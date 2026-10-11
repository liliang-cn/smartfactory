package adapters

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"

	"smartfactory/internal/config"
	"smartfactory/internal/mapping"
	"smartfactory/internal/tags"
)

// RunOPCUA reads nodes from an OPC UA server (a PLC's built-in server, Kepware, an edge gateway).
func RunOPCUA(ctx context.Context, src config.Source, hub *tags.Hub, log *slog.Logger) {
	log = log.With("adapter", src.Name)
	hub.Source(src.Name, "opcua", src.URL, len(src.Points))
	register(hub, src)

	ids := make([]*ua.NodeID, len(src.Points))
	for i, p := range src.Points {
		id, err := ua.ParseNodeID(p.Node)
		if err != nil {
			hub.SetConnected(src.Name, false, err)
			log.Error("bad node id", "node", p.Node, "err", err)
			return
		}
		ids[i] = id
	}

	var mu sync.Mutex
	var client *opcua.Client
	connect := func() error {
		if client != nil {
			return nil
		}
		opts := []opcua.Option{opcua.SecurityMode(ua.MessageSecurityModeNone), opcua.RequestTimeout(5 * time.Second)}
		if src.Username != "" {
			opts = append(opts, opcua.AuthUsername(src.Username, src.Password))
		} else {
			opts = append(opts, opcua.AuthAnonymous())
		}
		c, err := opcua.NewClient(src.URL, opts...)
		if err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := c.Connect(cctx); err != nil {
			return err
		}
		client = c
		log.Info("connected", "url", src.URL, "nodes", len(ids))
		return nil
	}
	fail := func(err error) {
		if client != nil {
			client.Close(context.Background())
			client = nil
		}
		hub.SetConnected(src.Name, false, err)
	}

	hub.OnCommand(func(c tags.Command) {
		if !Forward(src.Public, c) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, b := range src.Commands {
			if b.Node == "" || !mapping.CommandMatches(b, c) {
				continue
			}
			id, err := ua.ParseNodeID(b.Node)
			if err != nil {
				log.Warn("bad command node", "node", b.Node, "err", err)
				continue
			}
			if err := connect(); err != nil {
				fail(err)
				return
			}
			var val any = b.Value
			if b.Text != "" {
				val = b.Text
			} else if c.Arg != nil && b.Value == 0 {
				val = *c.Arg
			}
			writes := []*ua.WriteValue{}
			if b.ArgNode != "" && c.Arg != nil {
				if aid, err := ua.ParseNodeID(b.ArgNode); err == nil {
					writes = append(writes, &ua.WriteValue{NodeID: aid, AttributeID: ua.AttributeIDValue,
						Value: &ua.DataValue{EncodingMask: ua.DataValueValue, Value: ua.MustVariant(*c.Arg)}})
				}
			}
			// the command itself goes last, after its argument
			writes = append(writes, &ua.WriteValue{NodeID: id, AttributeID: ua.AttributeIDValue,
				Value: &ua.DataValue{EncodingMask: ua.DataValueValue, Value: ua.MustVariant(val)}})
			req := &ua.WriteRequest{NodesToWrite: writes}
			if _, err := client.Write(ctx, req); err != nil {
				log.Warn("write", "node", b.Node, "err", err)
				fail(err)
			}
		}
	})

	nodes := make([]*ua.ReadValueID, len(ids))
	for i, id := range ids {
		nodes[i] = &ua.ReadValueID{NodeID: id, AttributeID: ua.AttributeIDValue}
	}
	tick := time.NewTicker(src.Poll.Or(time.Second))
	defer tick.Stop()
	for {
		mu.Lock()
		if err := connect(); err != nil {
			fail(err)
		} else if resp, err := client.Read(ctx, &ua.ReadRequest{NodesToRead: nodes, TimestampsToReturn: ua.TimestampsToReturnBoth}); err != nil {
			fail(err)
		} else {
			hub.SetConnected(src.Name, true, nil)
			for i, r := range resp.Results {
				if i >= len(src.Points) || r.Status != ua.StatusOK || r.Value == nil {
					continue
				}
				p := src.Points[i]
				hub.Publish(tags.Tag{Company: p.Company, Plant: p.Plant, Asset: p.Asset, Field: p.Field, Value: mapping.Convert(r.Value.Value(), p), Source: src.Name})
			}
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
			mu.Lock()
			fail(nil)
			mu.Unlock()
			return
		case <-tick.C:
		}
	}
}
