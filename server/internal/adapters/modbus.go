package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/simonvetter/modbus"

	"smartfactory/internal/config"
	"smartfactory/internal/mapping"
	"smartfactory/internal/tags"
)

// RunModbus polls a Modbus TCP device (PLC, power meter, temperature controller).
func RunModbus(ctx context.Context, src config.Source, hub *tags.Hub, log *slog.Logger) {
	log = log.With("adapter", src.Name)
	hub.Source(src.Name, "modbus", src.URL, len(src.Points))
	client, err := modbus.NewClient(&modbus.ClientConfiguration{URL: src.URL, Timeout: 3 * time.Second})
	if err != nil {
		hub.SetConnected(src.Name, false, err)
		log.Error("bad modbus url", "err", err)
		return
	}
	unit := src.Unit
	if unit == 0 {
		unit = 1
	}
	var mu sync.Mutex // the client is not safe for concurrent use: polling and commands share it
	open := false
	ensure := func() error {
		if open {
			return nil
		}
		if err := client.Open(); err != nil {
			return err
		}
		open = true
		return client.SetUnitId(unit)
	}
	fail := func(err error) {
		if open {
			client.Close()
			open = false
		}
		hub.SetConnected(src.Name, false, err)
	}

	hub.OnCommand(func(c tags.Command) {
		mu.Lock()
		defer mu.Unlock()
		for _, b := range src.Commands {
			if b.Register == "" || !mapping.CommandMatches(b, c) {
				continue
			}
			if err := ensure(); err != nil {
				fail(err)
				return
			}
			var err error
			if b.Register == "coil" {
				err = client.WriteCoil(b.Address, b.Value != 0)
			} else {
				err = client.WriteRegister(b.Address, uint16(b.Value))
			}
			if err != nil {
				log.Warn("write", "addr", b.Address, "err", err)
				fail(err)
			} else {
				log.Info("command written", "cmd", c.Cmd, "asset", c.Asset, "register", b.Register, "addr", b.Address)
			}
		}
	})

	tick := time.NewTicker(src.Poll.Or(time.Second))
	defer tick.Stop()
	for {
		mu.Lock()
		if err := ensure(); err != nil {
			fail(err)
		} else {
			ok := true
			for _, p := range src.Points {
				v, err := readPoint(client, p)
				if err != nil {
					log.Warn("read", "register", p.Register, "addr", p.Address, "err", err)
					fail(err)
					ok = false
					break
				}
				hub.Publish(tags.Tag{Company: p.Company, Plant: p.Plant, Asset: p.Asset, Field: p.Field, Value: mapping.Convert(v, p), Source: src.Name})
			}
			if ok {
				hub.SetConnected(src.Name, true, nil)
			}
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
			client.Close()
			return
		case <-tick.C:
		}
	}
}

func readPoint(c *modbus.ModbusClient, p config.Point) (any, error) {
	switch p.Register {
	case "coil":
		return c.ReadCoil(p.Address)
	case "discrete":
		return c.ReadDiscreteInput(p.Address)
	case "holding", "input", "":
	default:
		return nil, fmt.Errorf("unknown register type %q", p.Register)
	}
	rt := modbus.HOLDING_REGISTER
	if p.Register == "input" {
		rt = modbus.INPUT_REGISTER
	}
	switch p.Kind {
	case "", "int16":
		v, err := c.ReadRegister(p.Address, rt)
		return float64(int16(v)), err
	case "uint16":
		v, err := c.ReadRegister(p.Address, rt)
		return float64(v), err
	case "int32":
		v, err := c.ReadUint32(p.Address, rt)
		return float64(int32(v)), err
	case "uint32":
		v, err := c.ReadUint32(p.Address, rt)
		return float64(v), err
	case "float32":
		v, err := c.ReadFloat32(p.Address, rt)
		return float64(v), err
	case "bool":
		v, err := c.ReadRegister(p.Address, rt)
		return v != 0, err
	}
	return nil, fmt.Errorf("unknown kind %q", p.Kind)
}
