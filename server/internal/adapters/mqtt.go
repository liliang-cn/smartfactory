package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"smartfactory/internal/config"
	"smartfactory/internal/mapping"
	"smartfactory/internal/tags"
)

// RunMQTT subscribes to an external broker (a plant's own broker, an IIoT gateway, a cloud
// broker) and maps its topics onto the twin.
func RunMQTT(ctx context.Context, src config.Source, hub *tags.Hub, log *slog.Logger) {
	log = log.With("adapter", src.Name)
	hub.Source(src.Name, "mqtt", src.URL, len(src.Points))

	handle := func(topic string, payload []byte) {
		if src.Convention != "" {
			if ts, err := mapping.Convention(src.Convention, topic, payload, src.Name); err == nil {
				for _, t := range ts {
					hub.Publish(t)
				}
			}
		}
		for _, p := range src.Points {
			caps, ok := mapping.MatchTopic(p.Topic, topic)
			if !ok {
				continue
			}
			v, ts, err := mapping.ExtractTimed(payload, p.JSON, p.Time)
			if err != nil {
				log.Debug("extract", "topic", topic, "err", err)
				continue
			}
			t := tags.Tag{
				Company: mapping.Expand(p.Company, caps), Plant: mapping.Expand(p.Plant, caps),
				Asset: mapping.Expand(p.Asset, caps), Field: mapping.Expand(p.Field, caps),
				Value: mapping.Convert(v, p), Source: src.Name,
			}
			mapping.Stamp(&t, ts, time.Now())
			hub.Publish(t)
		}
	}

	filters := map[string]byte{}
	for _, p := range src.Points {
		filters[p.Topic] = 1
	}
	if src.Convention != "" {
		filters[src.Convention+"/#"] = 1
	}

	cid := src.ClientID
	if cid == "" {
		cid = fmt.Sprintf("smartfactory-%s-%d", src.Name, time.Now().UnixNano()%1e6)
	}
	opts := paho.NewClientOptions().AddBroker(src.URL).SetClientID(cid).
		SetUsername(src.Username).SetPassword(src.Password).
		SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(5 * time.Second).
		SetOnConnectHandler(func(c paho.Client) {
			hub.SetConnected(src.Name, true, nil)
			if len(filters) > 0 {
				c.SubscribeMultiple(filters, func(_ paho.Client, m paho.Message) { handle(m.Topic(), m.Payload()) })
			}
			log.Info("connected", "url", src.URL, "filters", len(filters))
		}).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			hub.SetConnected(src.Name, false, err)
			log.Warn("connection lost", "err", err)
		})
	client := paho.NewClient(opts)
	client.Connect()

	hub.OnCommand(func(c tags.Command) {
		if !client.IsConnected() || !Forward(src.Public, c) {
			return
		}
		if src.Convention != "" {
			b, _ := json.Marshal(commandPayload(c))
			client.Publish(mapping.CommandTopic(src.Convention, c), 1, false, b)
		}
		for _, b := range src.Commands {
			if b.Topic == "" || !mapping.CommandMatches(b, c) {
				continue
			}
			arg := ""
			if c.Arg != nil {
				arg = fmt.Sprint(*c.Arg)
			}
			payload := strings.NewReplacer("{cmd}", c.Cmd, "{arg}", arg, "{asset}", c.Asset).Replace(b.Payload)
			client.Publish(b.Topic, 1, false, payload)
		}
	})

	<-ctx.Done()
	client.Disconnect(250)
}
