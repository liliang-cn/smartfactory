// Package devices emulates the equipment of every plant: an independent simulation of the
// physical machines (their own running, heating, wear, faults and repair crews) that never
// stops producing data, served per plant over its own MQTT client, Modbus TCP slave or OPC UA
// server following the SmartFactory conventions. The twin server reads them like any real
// equipment, and commands it sends come back here.
package devices

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"github.com/simonvetter/modbus"

	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/sfconv"
)

type Config struct {
	// How often equipment publishes / refreshes its registers and nodes (default 2s).
	Every  config.Duration `json:"every"`
	Plants []Plant         `json:"plants"`
}

type Plant struct {
	Company  string `json:"company"`
	Plant    string `json:"plant"`
	Protocol string `json:"protocol"` // mqtt | modbus | opcua
	// mqtt: broker to publish to, topic prefix of the convention
	Broker   string `json:"broker"`
	Username string `json:"username"`
	Password string `json:"password"`
	Prefix   string `json:"prefix"`
	// modbus: listen address
	Listen string `json:"listen"`
	// opcua: host name in the endpoint URL, and port
	Host string `json:"host"`
	Port int    `json:"port"`
	// asset/field pairs this emulator leaves out because real sensors provide them, e.g. HP-101/temp
	Skip []string `json:"skip"`
}

func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// endpoint is one plant's protocol surface.
type endpoint interface {
	update(t engine.Telemetry)
	close()
}

// Run starts the emulated equipment and keeps it publishing until ctx ends.
func Run(ctx context.Context, cfg Config, eng *engine.Engine, log *slog.Logger) error {
	go eng.Run(ctx)
	eng.PlayEquipment() // goes through the loop, so after it has started

	var eps []struct {
		p  Plant
		ep endpoint
	}
	for _, p := range cfg.Plants {
		machines := eng.Machines(p.Company, p.Plant)
		if len(machines) == 0 {
			return fmt.Errorf("%s/%s: no such plant, or it has no machines", p.Company, p.Plant)
		}
		command := func(asset, cmd string, arg *float64) {
			r := eng.Command(p.Company, p.Plant, asset, cmd, arg)
			log.Info("command", "plant", p.Company+"/"+p.Plant, "asset", asset, "cmd", cmd, "ok", r.OK, "msg", r.Msg)
		}
		var ep endpoint
		var err error
		switch p.Protocol {
		case "mqtt":
			ep, err = newMQTT(p, command, log)
		case "modbus":
			ep, err = newModbus(p, machines, command)
		case "opcua":
			ep, err = newOPCUA(ctx, p, machines, command, log)
		default:
			err = fmt.Errorf("unknown protocol %q", p.Protocol)
		}
		if err != nil {
			return fmt.Errorf("%s/%s: %w", p.Company, p.Plant, err)
		}
		defer ep.close()
		eps = append(eps, struct {
			p  Plant
			ep endpoint
		}{p, ep})
		log.Info("equipment up", "plant", p.Company+"/"+p.Plant, "protocol", p.Protocol, "machines", len(machines), "at", where(p))
	}

	tick := time.NewTicker(cfg.Every.Or(2 * time.Second))
	defer tick.Stop()
	for {
		for _, e := range eps {
			t, err := eng.Telemetry(e.p.Company, e.p.Plant)
			if err != nil {
				log.Warn("telemetry", "plant", e.p.Plant, "err", err)
				continue
			}
			t.Machines = skip(t.Machines, e.p.Skip)
			e.ep.update(t)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func where(p Plant) string {
	switch p.Protocol {
	case "mqtt":
		return p.Broker
	case "modbus":
		return "tcp://" + p.Listen
	}
	return fmt.Sprintf("opc.tcp://%s:%d", p.Host, p.Port)
}

// skip blanks fields that real sensors provide (NaN marks "not published").
func skip(ms []engine.MachineState, pairs []string) []engine.MachineState {
	for _, s := range pairs {
		asset, field, _ := strings.Cut(s, "/")
		for i := range ms {
			if ms[i].ID != asset {
				continue
			}
			switch field {
			case "temp":
				ms[i].Temp = math.NaN()
			case "power":
				ms[i].Power = math.NaN()
			case "vib":
				ms[i].Vib = math.NaN()
			}
		}
	}
	return ms
}

func parseArg(v any) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return &f
		}
	}
	return nil
}

// ---------- MQTT: one client per plant ----------

type mqttEP struct {
	p      Plant
	client paho.Client
}

func newMQTT(p Plant, command func(string, string, *float64), log *slog.Logger) (*mqttEP, error) {
	if p.Prefix == "" {
		p.Prefix = "sf"
	}
	cmdTopic := fmt.Sprintf("%s/%s/%s/+/cmd", p.Prefix, p.Company, p.Plant)
	opts := paho.NewClientOptions().AddBroker(p.Broker).SetClientID(fmt.Sprintf("dev-%s-%s", p.Company, p.Plant)).
		SetUsername(p.Username).SetPassword(p.Password).
		SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(5 * time.Second).
		SetOnConnectHandler(func(c paho.Client) {
			c.Subscribe(cmdTopic, 1, func(_ paho.Client, m paho.Message) {
				parts := strings.Split(m.Topic(), "/")
				var body struct {
					Cmd string `json:"cmd"`
					Arg any    `json:"arg"`
				}
				if len(parts) == 5 && json.Unmarshal(m.Payload(), &body) == nil && body.Cmd != "" {
					command(parts[3], body.Cmd, parseArg(body.Arg))
				}
			})
		}).
		SetConnectionLostHandler(func(_ paho.Client, err error) { log.Warn("mqtt lost", "plant", p.Plant, "err", err) })
	c := paho.NewClient(opts)
	c.Connect()
	return &mqttEP{p: p, client: c}, nil
}

func (e *mqttEP) update(t engine.Telemetry) {
	if !e.client.IsConnected() {
		return
	}
	base := fmt.Sprintf("%s/%s/%s/", e.p.Prefix, e.p.Company, e.p.Plant)
	for _, m := range t.Machines {
		body := map[string]any{"state": m.State, "fault": m.Fault, "speed": m.Speed, "health": m.Health}
		for k, v := range map[string]float64{"temp": m.Temp, "power": m.Power, "vib": m.Vib} {
			if !math.IsNaN(v) {
				body[k] = v
			}
		}
		b, _ := json.Marshal(body)
		e.client.Publish(base+m.ID, 0, false, b)
	}
	for _, r := range t.Readings {
		id, _ := r[0].(string)
		v, _ := r[1].(float64)
		e.client.Publish(base+id+"/measure", 0, false, strconv.FormatFloat(v, 'f', -1, 64))
	}
}

func (e *mqttEP) close() { e.client.Disconnect(250) }

// ---------- Modbus TCP: one slave per plant ----------

type modbusEP struct {
	srv      *modbus.ModbusServer
	mu       sync.Mutex
	regs     []uint16
	machines []sfconv.Machine
	command  func(string, string, *float64)
}

func newModbus(p Plant, machines []sfconv.Machine, command func(string, string, *float64)) (*modbusEP, error) {
	e := &modbusEP{regs: make([]uint16, len(machines)*sfconv.Regs), machines: machines, command: command}
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://" + p.Listen, MaxClients: 8, Timeout: 60 * time.Second}, e)
	if err != nil {
		return nil, err
	}
	if err := srv.Start(); err != nil {
		return nil, err
	}
	e.srv = srv
	return e, nil
}

func scaled(v, by float64) uint16 {
	if math.IsNaN(v) {
		return 0
	}
	return uint16(int16(math.Round(v * by)))
}

func (e *modbusEP) update(t engine.Telemetry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, m := range t.Machines {
		if i >= len(e.machines) {
			break
		}
		r := e.regs[i*sfconv.Regs:]
		r[sfconv.OffState] = sfconv.StateCode(m.State)
		r[sfconv.OffTemp] = scaled(m.Temp, 10)
		r[sfconv.OffPower] = uint16(math.Round(max(0, nanTo(m.Power)) * 10))
		r[sfconv.OffVib] = scaled(m.Vib, 100)
		r[sfconv.OffSpeed] = uint16(math.Round(m.Speed * 100))
		r[sfconv.OffHealth] = uint16(math.Round(m.Health * 1000))
		bits := math.Float32bits(float32(m.LastMeasure))
		r[sfconv.OffMeasure], r[sfconv.OffMeasure+1] = uint16(bits>>16), uint16(bits)
		r[sfconv.OffMCount] = uint16(m.MCount)
		r[sfconv.OffFault] = uint16(m.FaultCode)
	}
}

func nanTo(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return v
}

func (e *modbusEP) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	end := int(req.Addr) + int(req.Quantity)
	if end > len(e.regs) {
		return nil, modbus.ErrIllegalDataAddress
	}
	if req.IsWrite {
		var cmds []func()
		for k, v := range req.Args {
			addr := int(req.Addr) + k
			e.regs[addr] = v
			i, off := addr/sfconv.Regs, addr%sfconv.Regs
			if off == sfconv.OffCmd && v != 0 && int(v) < len(sfconv.Commands) && i < len(e.machines) {
				arg := float64(int16(e.regs[i*sfconv.Regs+sfconv.OffArg])) / 100
				asset, cmd := e.machines[i].ID, sfconv.Commands[v]
				var a *float64
				if cmd == "speed" {
					a = &arg
				}
				cmds = append(cmds, func() { e.command(asset, cmd, a) })
				e.regs[addr] = 0 // acknowledged
			}
		}
		// run the commands outside the lock
		go func() {
			for _, c := range cmds {
				c()
			}
		}()
		return req.Args, nil
	}
	return slices.Clone(e.regs[req.Addr:end]), nil
}

func (e *modbusEP) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	return e.HandleHoldingRegisters(&modbus.HoldingRegistersRequest{Addr: req.Addr, Quantity: req.Quantity, UnitId: req.UnitId})
}

func (e *modbusEP) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	return make([]bool, req.Quantity), nil
}

func (e *modbusEP) HandleDiscreteInputs(req *modbus.DiscreteInputsRequest) ([]bool, error) {
	return make([]bool, req.Quantity), nil
}

func (e *modbusEP) close() { e.srv.Stop() }

// ---------- OPC UA: one server per plant ----------

type opcuaEP struct {
	srv *server.Server
	ns  *sfconv.LockedNamespace
}

func newOPCUA(ctx context.Context, p Plant, machines []sfconv.Machine, command func(string, string, *float64), log *slog.Logger) (*opcuaEP, error) {
	srv := server.New(
		server.EndPoint(p.Host, p.Port),
		server.EnableSecurity("None", ua.MessageSecurityModeNone),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous),
	)
	ns := sfconv.NewLockedNamespace(srv, fmt.Sprintf("SmartFactory/%s/%s", p.Company, p.Plant))
	for _, m := range machines {
		for _, f := range []string{"state", "fault"} {
			ns.Data[sfconv.Key(m.ID, f)] = ""
		}
		for _, f := range []string{"temp", "power", "vib", "speed", "health", "faultCode", "lastMeasure", "mcount", "arg"} {
			ns.Data[sfconv.Key(m.ID, f)] = 0.0
		}
		ns.Data[sfconv.Key(m.ID, "cmd")] = ""
	}
	if err := srv.Start(ctx); err != nil {
		return nil, err
	}
	// Writes from clients arrive here: "{asset}.cmd" carries a command name.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case key := <-ns.ExternalNotification:
				asset, ok := strings.CutSuffix(key, ".cmd")
				if !ok {
					continue
				}
				cmd, _ := ns.GetValue(key).(string)
				if cmd == "" {
					continue
				}
				var a *float64
				if cmd == "speed" {
					a = parseArg(ns.GetValue(sfconv.Key(asset, "arg")))
				}
				command(asset, cmd, a)
				ns.Mu.Lock()
				ns.Data[key] = ""
				ns.Mu.Unlock()
			}
		}
	}()
	log.Debug("opc ua namespace", "plant", p.Plant, "ns", ns.ID())
	return &opcuaEP{srv: srv, ns: ns}, nil
}

func (e *opcuaEP) update(t engine.Telemetry) {
	set := func(asset, field string, v any) {
		if f, ok := v.(float64); ok && math.IsNaN(f) {
			return
		}
		e.ns.SetValue(sfconv.Key(asset, field), v)
	}
	for _, m := range t.Machines {
		set(m.ID, "state", m.State)
		set(m.ID, "fault", m.Fault)
		set(m.ID, "temp", m.Temp)
		set(m.ID, "power", m.Power)
		set(m.ID, "vib", m.Vib)
		set(m.ID, "speed", m.Speed)
		set(m.ID, "health", m.Health)
		set(m.ID, "faultCode", float64(m.FaultCode))
		set(m.ID, "lastMeasure", m.LastMeasure)
		set(m.ID, "mcount", float64(m.MCount))
	}
}

func (e *opcuaEP) close() { e.srv.Close() }
