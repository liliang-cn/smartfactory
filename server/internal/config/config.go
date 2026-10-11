// Package config is the server's JSON configuration. Everything is optional: with no file the
// server serves the twin, runs an embedded MQTT broker and accepts the topic convention
//
//	{prefix}/{company}/{plant}/{asset}/{field}     payload: a number, a string, or {"v": ...}
//	{prefix}/{company}/{plant}/{asset}             payload: {"temp": 61.5, "power": 230, ...}
//
// so a device that follows the convention needs no configuration at all. Sources describe
// everything else: external brokers with their own topic layout, Modbus TCP registers and
// OPC UA nodes, each mapped onto company/plant/asset/field.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Config struct {
	// HTTP listen address for the web app, the live feed and MCP.
	HTTP string `json:"http"`
	// Token is the original single token: whoever holds it is an administrator of every company.
	// SF_TOKEN in the environment overrides it. Empty means no such token.
	Token string `json:"token"`
	// Tokens are people's and clients' own tokens, each with a role and the companies it covers
	// (`smartfactory token` makes one). Only a hash of each is kept here.
	Tokens []Key  `json:"tokens"`
	MQTT   Broker `json:"mqtt"`
	// Data is where the simulation's state is kept across restarts ("" disables it).
	Data string `json:"data"`
	// Sources are protocol adapters that read from devices.
	Sources []Source `json:"sources"`
	// History keeps companies' production history in Postgres, for the advisor.
	History []History `json:"history"`
	// What the server runs:
	//   - by default, one company: a self-hosted instance starts empty, its administrator builds
	//     the factory from a template, a description or a config, and that company is all it runs;
	//   - Demo: every built-in template, as a company each (development, the public demo);
	//   - Companies: just these ([] for none: the platform server, which lists the instances).
	Demo      bool     `json:"demo,omitempty"`
	Companies []string `json:"companies,omitempty"`
	// Instances are the customers' own servers. A factory page asked of one of them here is sent
	// there, and the console lists them.
	Instances []Instance `json:"instances,omitempty"`
	// TemplateHub is the public template hub (default https://factory.superleo.cn; "off" for
	// none); HubToken this instance's account there, to publish (${VAR} from the environment).
	TemplateHub string `json:"templateHub,omitempty"`
	HubToken    string `json:"hubToken,omitempty"`
	// Hub: this server is the template hub, keeping what anyone publishes.
	Hub bool `json:"hub,omitempty"`
}

// Instance is one customer's own server.
type Instance struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// URL is where people open it (https://huaduan.factory.superleo.cn).
	URL string `json:"url"`
	// Internal is where this server reaches it to see whether it is up (http://127.0.0.1:44731).
	Internal string `json:"internal,omitempty"`
}

// Key is one person's or client's token.
type Key struct {
	// Name is who holds it, as the audit log and the people watching show it.
	Name string `json:"name"`
	// member | operator | admin
	Role string `json:"role"`
	// Companies it covers; "*" or none means every company.
	Companies []string `json:"companies,omitempty"`
	// SHA256 is the hex SHA-256 of the token.
	SHA256 string `json:"sha256"`
	// Expires (YYYY-MM-DD, optional) is the last day it works.
	Expires string `json:"expires,omitempty"`
}

// History is one company's history database.
type History struct {
	Company string `json:"company"`
	// postgres://user:pass@host/db; ${VAR} is taken from the environment
	DSN string `json:"dsn"`
	// Advisor is the URL of the advisor console that reads this history (shown in the app)
	Advisor string `json:"advisor,omitempty"`
	// AdvisorAPI is the advisor service the twin calls for its viewers; AdvisorToken is the identity
	// their reads and questions go out as, ApproverToken the one operators' decisions go out as
	// (${VAR} is taken from the environment).
	AdvisorAPI    string `json:"advisorApi,omitempty"`
	AdvisorToken  string `json:"advisorToken,omitempty"`
	ApproverToken string `json:"approverToken,omitempty"`
}

// Broker is the embedded MQTT broker.
type Broker struct {
	// Listen address; empty disables the broker.
	Listen string `json:"listen"`
	// Prefix of the topic convention.
	Prefix string `json:"prefix"`
	// Users with passwords; empty allows anonymous clients.
	Users map[string]string `json:"users"`
	// Public: devices on this broker are emulated, so visitors' commands are published too.
	Public bool `json:"public"`
}

type Source struct {
	Name string `json:"name"`
	// mqtt | modbus | opcua
	Type string `json:"type"`
	// tcp://host:1883 · tcp://host:502 · opc.tcp://host:4840
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	ClientID string `json:"clientId"`
	// mqtt: also accept convention topics under this prefix from this broker.
	// modbus/opcua: "sf" derives every point from the plant's machines (see package sfconv).
	Convention string `json:"convention"`
	// modbus/opcua with the "sf" convention: the plant the device serves
	Company string `json:"company"`
	Plant   string `json:"plant"`
	// Public sources are emulated equipment: visitors' commands reach them too.
	Public bool `json:"public"`
	// modbus: unit (slave) id
	Unit uint8 `json:"unit"`
	// modbus/opcua: polling interval, e.g. "1s"
	Poll     Duration     `json:"poll"`
	Points   []Point      `json:"points"`
	Commands []CmdBinding `json:"commands"`
}

// Point maps one protocol address onto an asset field.
type Point struct {
	// mqtt: topic filter; "+" segments are captured as {1}, {2}, … for use in the target
	Topic string `json:"topic"`
	// mqtt: dotted path into a JSON payload, e.g. "data.temperature"; empty = whole payload
	JSON string `json:"json"`
	// modbus: holding | input | coil | discrete
	Register string `json:"register"`
	Address  uint16 `json:"address"`
	// modbus: int16 | uint16 | int32 | uint32 | float32 | bool (default int16, bool for coils)
	Kind string `json:"kind"`
	// opcua: node id, e.g. ns=2;s=Press1.Temperature
	Node string `json:"node"`

	Company string `json:"company"`
	Plant   string `json:"plant"`
	Asset   string `json:"asset"`
	Field   string `json:"field"`

	// value = raw * scale + offset; map translates enumerated raw values (e.g. "2": "fault")
	Scale  *float64       `json:"scale"`
	Offset *float64       `json:"offset"`
	Map    map[string]any `json:"map"`
}

// CmdBinding turns an operator command into a protocol write.
type CmdBinding struct {
	Company string `json:"company"`
	Plant   string `json:"plant"`
	Asset   string `json:"asset"`
	// start | stop | reset | maint | speed | …; "*" matches any
	Cmd string `json:"cmd"`
	// mqtt
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
	// modbus
	Register string  `json:"register"`
	Address  uint16  `json:"address"`
	Value    float64 `json:"value"`
	// modbus: the command's argument is written here first, ×ArgScale
	ArgAddress *uint16 `json:"argAddress"`
	ArgScale   float64 `json:"argScale"`
	// opcua: Text (if set) is written instead of Value; the argument goes to ArgNode
	Node    string `json:"node"`
	Text    string `json:"text"`
	ArgNode string `json:"argNode"`
}

// Duration reads "1s", "500ms" from JSON.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		d.Duration = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	d.Duration = v
	return err
}

// MarshalJSON writes it the way it is read ("1s"), so a config written out reads back.
func (d Duration) MarshalJSON() ([]byte, error) {
	if d.Duration == 0 {
		return []byte(`""`), nil
	}
	return json.Marshal(d.Duration.String())
}

func (d Duration) Or(def time.Duration) time.Duration {
	if d.Duration <= 0 {
		return def
	}
	return d.Duration
}

// Default is a server started without a config file: the demo, so trying it shows something.
func Default() Config {
	return Config{HTTP: ":4720", Data: "data", MQTT: Broker{Listen: ":41883", Prefix: "sf"}, Demo: true}
}

// Load reads path (if any) over the defaults.
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		c.Demo = false // a config file says what it runs
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	}
	if t := os.Getenv("SF_TOKEN"); t != "" {
		c.Token = t
	}
	switch c.TemplateHub {
	case "":
		c.TemplateHub = "https://factory.superleo.cn"
	case "off":
		c.TemplateHub = ""
	}
	c.HubToken = os.ExpandEnv(c.HubToken)
	for i := range c.History {
		c.History[i].DSN = os.ExpandEnv(c.History[i].DSN)
		c.History[i].AdvisorToken = os.ExpandEnv(c.History[i].AdvisorToken)
		c.History[i].ApproverToken = os.ExpandEnv(c.History[i].ApproverToken)
	}
	if c.MQTT.Prefix == "" {
		c.MQTT.Prefix = "sf"
	}
	for i, s := range c.Sources {
		if s.Name == "" {
			c.Sources[i].Name = fmt.Sprintf("%s-%d", s.Type, i+1)
		}
		switch s.Type {
		case "mqtt", "modbus", "opcua":
		default:
			return c, fmt.Errorf("source %q: unknown type %q (mqtt, modbus, opcua)", s.Name, s.Type)
		}
	}
	return c, nil
}
