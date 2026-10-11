// Package sfconv is the SmartFactory convention for equipment that speaks Modbus TCP or OPC UA:
// where each machine's values live, derived from the plant's machine list (the same config on
// both ends), so a plant needs no hand-written register or node map.
//
// Modbus: machine i occupies holding registers [i*Regs, i*Regs+Regs):
//
//	+0 state (index into States)   +1 temp ×10 (int16)      +2 power ×10 (uint16)
//	+3 vib ×100 (int16)            +4 speed ×100            +5 health ×1000
//	+6..7 last measured value (float32)                     +8 measured-unit counter
//	+9 fault code (0 none, n = n-th fault of the machine)   +10 command code (written by the twin)
//	+11 command argument ×100                               +12 tool use counter   +13 tool life
//
// After the machines come the vehicles, VRegs each:
//
//	+0 x ×10 (int16, metres in plant coordinates)   +1 z ×10   +2 heading ×1000 (rad)   +3 battery ×10
//
// and then the inventory items, two registers each: available, capacity.
//
// OPC UA: namespace 1, string node ids "{asset}.{field}" for the same fields (state and fault
// as strings), plus writable "{asset}.cmd" (command name) and "{asset}.arg"; vehicles
// "{vehicle}.x/z/heading/battery/task"; inventory "{warehouse}.inv.{key}" and "{warehouse}.cap.{key}".
package sfconv

import (
	"fmt"

	"smartfactory/internal/config"
)

const Regs = 14

const (
	OffState = iota
	OffTemp
	OffPower
	OffVib
	OffSpeed
	OffHealth
	OffMeasure // two registers
	_
	OffMCount
	OffFault
	OffCmd
	OffArg
	OffToolUsed
	OffToolLife
)

var States = []string{"idle", "running", "blocked", "stopped", "fault", "maint", "changeover"}

// Commands by code; 0 means none.
var Commands = []string{"", "start", "stop", "reset", "maint", "speed", "fault", "retool", "toollimit"}

func StateCode(s string) uint16 {
	for i, v := range States {
		if v == s {
			return uint16(i)
		}
	}
	return 0
}

func CommandCode(c string) uint16 {
	for i, v := range Commands {
		if v == c && i > 0 {
			return uint16(i)
		}
	}
	return 0
}

const VRegs = 6

// Fleet is what the convention needs to know about a plant's vehicles and inventory.
type Fleet struct {
	Vehicles []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"vehicles"`
	Inventory []struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Unit  string `json:"unit"`
	} `json:"inventory"`
	Warehouse string `json:"warehouse"`
}

// VehicleBase is the first register of the vehicle block.
func VehicleBase(machines int) uint16 { return uint16(machines * Regs) }

// InventoryBase is the first register of the inventory block.
func InventoryBase(machines, vehicles int) uint16 { return uint16(machines*Regs + vehicles*VRegs) }

// Machine is what the convention needs to know about a machine.
type Machine struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Faults   []string `json:"faults"`
	Measured bool     `json:"measured"`
	Tooling  bool     `json:"tooling"`
	// Trip is the temperature (°C) it stops at, Power its running power (kW), Vib its vibration
	// (mm/s) at full speed; Measure the spec of what it measures. Zero when it has none.
	Trip    float64      `json:"trip,omitempty"`
	Power   float64      `json:"power,omitempty"`
	Vib     float64      `json:"vib,omitempty"`
	Measure *MeasureSpec `json:"measure,omitempty"`
}

// MeasureSpec is what a machine measures on each unit: the target, the limits, the spread.
type MeasureSpec struct {
	Target float64  `json:"target"`
	Lo     *float64 `json:"lo,omitempty"`
	Hi     *float64 `json:"hi,omitempty"`
	Sigma  float64  `json:"sigma"`
}

// Node is the OPC UA node id of a machine field.
func Node(asset, field string) string { return fmt.Sprintf("ns=1;s=%s.%s", asset, field) }

// Key is the node's string id inside the namespace.
func Key(asset, field string) string { return asset + "." + field }

func f(v float64) *float64 { return &v }

// Expand fills a modbus/opcua source that declares `"convention": "sf"` with points and
// commands for every machine of its plant. Fields are listed alphabetically so that, within one
// poll, a fault code precedes the state and the last reading precedes its counter.
func Expand(src config.Source, machines []Machine, fleet Fleet) config.Source {
	stateMap := map[string]any{}
	for i, s := range States {
		stateMap[fmt.Sprint(i)] = s
	}
	at := func(m Machine, field string) config.Point {
		return config.Point{Company: src.Company, Plant: src.Plant, Asset: m.ID, Field: field}
	}
	src.Points, src.Commands = nil, nil
	for i, m := range machines {
		base := uint16(i * Regs)
		switch src.Type {
		case "modbus":
			reg := func(field string, off uint16, kind string, scale *float64) config.Point {
				p := at(m, field)
				p.Register, p.Address, p.Kind, p.Scale = "holding", base+off, kind, scale
				return p
			}
			src.Points = append(src.Points, reg("faultCode", OffFault, "uint16", nil), reg("health", OffHealth, "uint16", f(0.001)))
			if m.Measured {
				src.Points = append(src.Points, reg("lastMeasure", OffMeasure, "float32", nil), reg("mcount", OffMCount, "uint16", nil))
			}
			st := reg("state", OffState, "uint16", nil)
			st.Map = stateMap
			src.Points = append(src.Points, reg("power", OffPower, "uint16", f(0.1)), reg("speed", OffSpeed, "uint16", f(0.01)), st,
				reg("temp", OffTemp, "int16", f(0.1)))
			if m.Tooling {
				src.Points = append(src.Points, reg("toolLife", OffToolLife, "uint16", nil), reg("toolUsed", OffToolUsed, "uint16", nil))
			}
			src.Points = append(src.Points, reg("vib", OffVib, "int16", f(0.01)))
			arg := base + OffArg
			for _, c := range Commands[1:] {
				src.Commands = append(src.Commands, config.CmdBinding{Company: src.Company, Plant: src.Plant, Asset: m.ID, Cmd: c,
					Register: "holding", Address: base + OffCmd, Value: float64(CommandCode(c)), ArgAddress: &arg, ArgScale: 100})
			}
		case "opcua":
			node := func(field string) config.Point {
				p := at(m, field)
				p.Node = Node(m.ID, field)
				return p
			}
			src.Points = append(src.Points, node("fault"), node("health"))
			if m.Measured {
				src.Points = append(src.Points, node("lastMeasure"), node("mcount"))
			}
			src.Points = append(src.Points, node("power"), node("speed"), node("state"), node("temp"))
			if m.Tooling {
				src.Points = append(src.Points, node("toolLife"), node("toolUsed"))
			}
			src.Points = append(src.Points, node("vib"))
			for _, c := range Commands[1:] {
				src.Commands = append(src.Commands, config.CmdBinding{Company: src.Company, Plant: src.Plant, Asset: m.ID, Cmd: c,
					Node: Node(m.ID, "cmd"), Text: c, ArgNode: Node(m.ID, "arg")})
			}
		}
	}
	at2 := func(asset, field string) config.Point {
		return config.Point{Company: src.Company, Plant: src.Plant, Asset: asset, Field: field}
	}
	vb := VehicleBase(len(machines))
	ib := InventoryBase(len(machines), len(fleet.Vehicles))
	for i, v := range fleet.Vehicles {
		switch src.Type {
		case "modbus":
			base := vb + uint16(i*VRegs)
			for _, r := range []struct {
				field string
				off   uint16
				kind  string
				scale float64
			}{{"battery", 3, "uint16", 0.1}, {"heading", 2, "int16", 0.001}, {"x", 0, "int16", 0.1}, {"z", 1, "int16", 0.1}} {
				p := at2(v.ID, r.field)
				p.Register, p.Address, p.Kind, p.Scale = "holding", base+r.off, r.kind, f(r.scale)
				src.Points = append(src.Points, p)
			}
		case "opcua":
			for _, field := range []string{"battery", "heading", "task", "x", "z"} {
				p := at2(v.ID, field)
				p.Node = Node(v.ID, field)
				src.Points = append(src.Points, p)
			}
		}
	}
	for i, it := range fleet.Inventory {
		switch src.Type {
		case "modbus":
			for off, field := range []string{"inv." + it.Key, "cap." + it.Key} {
				p := at2(fleet.Warehouse, field)
				p.Register, p.Address, p.Kind = "holding", ib+uint16(i*2+off), "uint16"
				src.Points = append(src.Points, p)
			}
		case "opcua":
			for _, field := range []string{"cap." + it.Key, "inv." + it.Key} {
				p := at2(fleet.Warehouse, field)
				p.Node = Node(fleet.Warehouse, field)
				src.Points = append(src.Points, p)
			}
		}
	}
	return src
}
