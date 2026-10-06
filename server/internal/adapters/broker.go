// Package adapters connects industrial protocols to the tag hub.
package adapters

import (
	"encoding/json"
	"log/slog"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"smartfactory/internal/config"
	"smartfactory/internal/mapping"
	"smartfactory/internal/tags"
)

// StartBroker runs the embedded MQTT broker. Devices publish to it using the topic convention,
// and operator commands are published back on {prefix}/{company}/{plant}/{asset}/cmd.
func StartBroker(cfg config.Broker, hub *tags.Hub, log *slog.Logger) (*mqtt.Server, error) {
	const name = "broker"
	s := mqtt.New(&mqtt.Options{InlineClient: true, Logger: log.With("adapter", name)})
	if len(cfg.Users) == 0 {
		if err := s.AddHook(new(auth.AllowHook), nil); err != nil {
			return nil, err
		}
	} else {
		users := auth.Users{}
		for u, p := range cfg.Users {
			users[u] = auth.UserRule{Username: auth.RString(u), Password: auth.RString(p)}
		}
		if err := s.AddHook(new(auth.Hook), &auth.Options{Ledger: &auth.Ledger{Users: users}}); err != nil {
			return nil, err
		}
	}
	if err := s.AddListener(listeners.NewTCP(listeners.Config{ID: "tcp", Address: cfg.Listen})); err != nil {
		return nil, err
	}
	go func() {
		if err := s.Serve(); err != nil {
			log.Error("mqtt broker stopped", "err", err)
		}
	}()

	hub.Source(name, "mqtt", "mqtt://"+cfg.Listen, 0)
	hub.SetConnected(name, true, nil)
	err := s.Subscribe(cfg.Prefix+"/#", 1, func(_ *mqtt.Client, _ packets.Subscription, pk packets.Packet) {
		ts, err := mapping.Convention(cfg.Prefix, pk.TopicName, pk.Payload, name)
		if err != nil {
			log.Debug("ignored message", "topic", pk.TopicName, "err", err)
			return
		}
		for _, t := range ts {
			hub.Publish(t)
		}
	})
	if err != nil {
		return nil, err
	}
	hub.OnCommand(func(c tags.Command) {
		payload, _ := json.Marshal(commandPayload(c))
		if err := s.Publish(mapping.CommandTopic(cfg.Prefix, c), payload, false, 1); err != nil {
			log.Warn("publish command", "err", err)
		}
	})
	log.Info("mqtt broker listening", "addr", cfg.Listen, "convention", cfg.Prefix+"/{company}/{plant}/{asset}/{field}")
	return s, nil
}

func commandPayload(c tags.Command) map[string]any {
	m := map[string]any{"cmd": c.Cmd, "by": c.By, "t": time.Now().UnixMilli()}
	if c.Arg != nil {
		m["arg"] = *c.Arg
	}
	return m
}
