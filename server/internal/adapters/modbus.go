package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
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
		if !Forward(src.Public, c) {
			return
		}
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
			if b.ArgAddress != nil && c.Arg != nil {
				scale := b.ArgScale
				if scale == 0 {
					scale = 1
				}
				err = client.WriteRegister(*b.ArgAddress, uint16(int16(*c.Arg*scale)))
			}
			// the command itself goes last, after its argument
			if err == nil {
				if b.Register == "coil" {
					err = client.WriteCoil(b.Address, b.Value != 0)
				} else {
					err = client.WriteRegister(b.Address, uint16(b.Value))
				}
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
		} else if blocks, err := readBlocks(client, src.Points); err != nil {
			log.Warn("read", "err", err)
			fail(err)
		} else {
			ok := true
			for _, p := range src.Points {
				v, err := pointValue(client, blocks, p)
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

// registers holds one poll's worth of holding/input registers, read in a few large requests.
type registers map[modbus.RegType]map[uint16]uint16

func width(kind string) uint16 {
	if kind == "int32" || kind == "uint32" || kind == "float32" {
		return 2
	}
	return 1
}

// readBlocks reads every register the points need with as few requests as possible
// (contiguous runs of at most 120 registers).
func readBlocks(c *modbus.ModbusClient, points []config.Point) (registers, error) {
	want := map[modbus.RegType]map[uint16]bool{}
	for _, p := range points {
		rt, ok := regType(p.Register)
		if !ok {
			continue
		}
		if want[rt] == nil {
			want[rt] = map[uint16]bool{}
		}
		for i := uint16(0); i < width(p.Kind); i++ {
			want[rt][p.Address+i] = true
		}
	}
	out := registers{}
	for rt, addrs := range want {
		sorted := make([]uint16, 0, len(addrs))
		for a := range addrs {
			sorted = append(sorted, a)
		}
		slices.Sort(sorted)
		out[rt] = map[uint16]uint16{}
		for i := 0; i < len(sorted); {
			start, end := sorted[i], sorted[i]
			j := i + 1
			for j < len(sorted) && sorted[j] == end+1 && sorted[j]-start < 120 {
				end = sorted[j]
				j++
			}
			vals, err := c.ReadRegisters(start, end-start+1, rt)
			if err != nil {
				return nil, err
			}
			for k, v := range vals {
				out[rt][start+uint16(k)] = v
			}
			i = j
		}
	}
	return out, nil
}

func regType(r string) (modbus.RegType, bool) {
	switch r {
	case "holding", "":
		return modbus.HOLDING_REGISTER, true
	case "input":
		return modbus.INPUT_REGISTER, true
	}
	return 0, false
}

// pointValue decodes a point from the block read (big-endian words, high word first, which is
// the library's default) or reads coils and discrete inputs directly.
func pointValue(c *modbus.ModbusClient, b registers, p config.Point) (any, error) {
	switch p.Register {
	case "coil":
		return c.ReadCoil(p.Address)
	case "discrete":
		return c.ReadDiscreteInput(p.Address)
	}
	rt, ok := regType(p.Register)
	if !ok {
		return nil, fmt.Errorf("unknown register type %q", p.Register)
	}
	r := b[rt]
	hi, lo := r[p.Address], r[p.Address+1]
	switch p.Kind {
	case "", "int16":
		return float64(int16(hi)), nil
	case "uint16":
		return float64(hi), nil
	case "int32":
		return float64(int32(uint32(hi)<<16 | uint32(lo))), nil
	case "uint32":
		return float64(uint32(hi)<<16 | uint32(lo)), nil
	case "float32":
		return float64(math.Float32frombits(uint32(hi)<<16 | uint32(lo))), nil
	case "bool":
		return hi != 0, nil
	}
	return nil, fmt.Errorf("unknown kind %q", p.Kind)
}
