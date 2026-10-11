// Package adapters connects industrial protocols to the tag hub.
package adapters

import (
	"encoding/json"
	"errors"
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

	// Every publishing client is its own data source, named by its client id; the hook sees the
	// publisher (an inline subscription would only see itself).
	if err := s.AddHook(&clientStatus{hub: hub, prefix: cfg.Prefix, url: "mqtt://" + cfg.Listen, log: log, public: cfg.Public}, nil); err != nil {
		return nil, err
	}
	hub.OnCommand(func(c tags.Command) {
		if !Forward(cfg.Public, c) {
			return
		}
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

// clientStatus turns each client's convention messages into tags under its own source, and
// marks that source offline when the client disconnects.
type clientStatus struct {
	mqtt.HookBase
	hub    *tags.Hub
	prefix string
	url    string
	log    *slog.Logger
	// public: the broker's devices are emulated
	public bool
}

func (h *clientStatus) ID() string { return "smartfactory-client-status" }

func (h *clientStatus) Provides(b byte) bool {
	return b == mqtt.OnPublish || b == mqtt.OnSessionEstablished || b == mqtt.OnDisconnect
}

func (h *clientStatus) OnPublish(cl *mqtt.Client, pk packets.Packet) (packets.Packet, error) {
	if cl == nil || cl.Net.Inline {
		return pk, nil
	}
	ts, err := mapping.Convention(h.prefix, pk.TopicName, pk.Payload, cl.ID)
	if err != nil {
		h.log.Debug("ignored message", "topic", pk.TopicName, "err", err)
		return pk, nil
	}
	if len(ts) > 0 {
		if !h.hub.HasSource(cl.ID) {
			h.hub.Source(cl.ID, "mqtt", h.url, 0)
			h.hub.SetPublic(cl.ID, h.public)
		}
		h.hub.SetConnected(cl.ID, true, nil)
	}
	for _, t := range ts {
		h.hub.Publish(t)
	}
	return pk, nil
}

func (h *clientStatus) OnSessionEstablished(cl *mqtt.Client, _ packets.Packet) {
	if h.hub.HasSource(cl.ID) {
		h.hub.SetConnected(cl.ID, true, nil)
	}
}

func (h *clientStatus) OnDisconnect(cl *mqtt.Client, err error, _ bool) {
	if h.hub.HasSource(cl.ID) {
		if err == nil {
			err = errors.New("disconnected")
		}
		h.hub.SetConnected(cl.ID, false, err)
	}
}
