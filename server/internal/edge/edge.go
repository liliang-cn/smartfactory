// Package edge is a small protocol gateway: it scrapes real measurements (any Prometheus text
// endpoint, e.g. node_exporter's hwmon temperatures) and serves them the way plant equipment
// does — published over MQTT, held in Modbus TCP registers, and exposed as OPC UA variables.
// The twin server then reads them back through its ordinary protocol adapters.
package edge

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
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
	"smartfactory/internal/sfconv"
)

type Config struct {
	// Prometheus text endpoints to scrape.
	Scrape []string        `json:"scrape"`
	Every  config.Duration `json:"every"`
	// Named measurements.
	Metrics map[string]Selector `json:"metrics"`
	MQTT    *struct {
		// tcp://host:41883, or tls://host:48883 for the broker's TLS listener
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
		// ClientID defaults to smartfactory-edge-{host}, or with Cert to the name the
		// certificate gives (a registered device connects under its own name).
		ClientID string `json:"clientId"`
		// Cert and Key are PEM files of the gateway's client certificate (a device's, from
		// the console or `smartfactory device add -cert`): it needs no password then. CA is
		// the certificate the broker's is checked against (the bundle's ca.crt); without it,
		// the system's.
		Cert    string `json:"cert"`
		Key     string `json:"key"`
		CA      string `json:"ca"`
		Publish []struct {
			Metric string `json:"metric"`
			Topic  string `json:"topic"`
		} `json:"publish"`
		// Buffer is the file readings wait in while the broker cannot be reached (default
		// edge-buffer.jsonl in the working directory); they are sent with the time they were
		// taken once it can. BufferMax readings at most (default 100000), the oldest dropped first.
		Buffer    string `json:"buffer"`
		BufferMax int    `json:"bufferMax"`
	} `json:"mqtt"`
	Modbus *struct {
		Listen string `json:"listen"`
		// value is stored as int16(round(metric * scale))
		Holding []struct {
			Metric  string  `json:"metric"`
			Address uint16  `json:"address"`
			Scale   float64 `json:"scale"`
		} `json:"holding"`
	} `json:"modbus"`
	OPCUA *struct {
		// host name clients use in the endpoint URL, and the port
		Host  string `json:"host"`
		Port  int    `json:"port"`
		Nodes []struct {
			Metric string `json:"metric"`
			Node   string `json:"node"`
		} `json:"nodes"`
	} `json:"opcua"`
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
	if len(c.Scrape) == 0 || len(c.Metrics) == 0 {
		return c, fmt.Errorf("%s: scrape and metrics are required", path)
	}
	if m := c.MQTT; m != nil && (m.Cert != "" || m.Key != "" || m.CA != "") {
		if (m.Cert == "") != (m.Key == "") {
			return c, fmt.Errorf("%s: mqtt.cert and mqtt.key go together", path)
		}
		if !strings.Contains(m.URL, "://") || !slices.Contains([]string{"tls", "ssl", "mqtts", "tcps"}, m.URL[:strings.Index(m.URL, "://")]) {
			return c, fmt.Errorf("%s: with mqtt.cert or mqtt.ca the url is tls://host:port (the broker's TLS listener)", path)
		}
	}
	return c, nil
}

// clientTLS is the gateway's TLS settings for the broker, and the client id its certificate
// names ("" without one).
func clientTLS(cert, key, ca string) (*tls.Config, string, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	name := ""
	if cert != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, "", fmt.Errorf("mqtt cert: %w", err)
		}
		tc.Certificates = []tls.Certificate{pair}
		if leaf, err := x509.ParseCertificate(pair.Certificate[0]); err == nil {
			name = leaf.Subject.CommonName
		}
	}
	if ca != "" {
		b, err := os.ReadFile(ca)
		if err != nil {
			return nil, "", fmt.Errorf("mqtt ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(b) {
			return nil, "", fmt.Errorf("mqtt ca: %s has no PEM certificate", ca)
		}
		tc.RootCAs = pool
	}
	return tc, name, nil
}

// Run scrapes on a timer and keeps the three protocol surfaces up to date.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	var (
		mu     sync.RWMutex
		values = map[string]float64{}
	)
	get := func(m string) (float64, bool) {
		mu.RLock()
		defer mu.RUnlock()
		v, ok := values[m]
		return v, ok
	}

	if cfg.Modbus != nil {
		h := &registers{get: get, cfg: cfg, coils: map[uint16]bool{}, log: log}
		srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://" + cfg.Modbus.Listen, MaxClients: 16, Timeout: 30 * time.Second}, h)
		if err != nil {
			return err
		}
		if err := srv.Start(); err != nil {
			return fmt.Errorf("modbus: %w", err)
		}
		defer srv.Stop()
		log.Info("modbus tcp slave listening", "addr", cfg.Modbus.Listen, "holding", len(cfg.Modbus.Holding))
	}

	var ns *sfconv.LockedNamespace
	if cfg.OPCUA != nil {
		srv := server.New(
			server.EndPoint(cfg.OPCUA.Host, cfg.OPCUA.Port),
			server.EnableSecurity("None", ua.MessageSecurityModeNone),
			server.EnableAuthMode(ua.UserTokenTypeAnonymous),
		)
		ns = sfconv.NewLockedNamespace(srv, "SmartFactoryEdge")
		for _, n := range cfg.OPCUA.Nodes {
			ns.Data[n.Node] = 0.0
		}
		if err := srv.Start(ctx); err != nil {
			return fmt.Errorf("opcua: %w", err)
		}
		defer srv.Close()
		log.Info("opc ua server listening", "endpoint", fmt.Sprintf("opc.tcp://%s:%d", cfg.OPCUA.Host, cfg.OPCUA.Port), "namespace", ns.ID(), "nodes", len(cfg.OPCUA.Nodes))
	}

	var mc paho.Client
	var buf *Buffer
	if cfg.MQTT != nil {
		path := cfg.MQTT.Buffer
		if path == "" {
			path = "edge-buffer.jsonl"
		}
		var err error
		if buf, err = OpenBuffer(path, cfg.MQTT.BufferMax); err != nil {
			return fmt.Errorf("mqtt buffer: %w", err)
		}
		if n := buf.Len(); n > 0 {
			log.Info("mqtt buffer: readings waiting from before", "readings", n, "file", path)
		}
		host, _ := os.Hostname()
		id := cfg.MQTT.ClientID
		opts := paho.NewClientOptions()
		if m := cfg.MQTT; m.Cert != "" || m.CA != "" {
			tc, name, err := clientTLS(m.Cert, m.Key, m.CA)
			if err != nil {
				return err
			}
			opts.SetTLSConfig(tc)
			if id == "" {
				id = name
			}
		}
		if id == "" {
			id = "smartfactory-edge-" + host
		}
		opts.AddBroker(cfg.MQTT.URL).SetClientID(id).
			SetUsername(cfg.MQTT.Username).SetPassword(cfg.MQTT.Password).
			SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(5 * time.Second).
			SetOnConnectHandler(func(paho.Client) { log.Info("mqtt connected", "url", cfg.MQTT.URL) }).
			SetConnectionLostHandler(func(_ paho.Client, err error) { log.Warn("mqtt lost", "err", err) })
		mc = paho.NewClient(opts)
		mc.Connect()
		defer mc.Disconnect(250)
	}

	tick := time.NewTicker(cfg.Every.Or(2 * time.Second))
	defer tick.Stop()
	for {
		var all []Series
		for _, u := range cfg.Scrape {
			s, err := Scrape(ctx, u)
			if err != nil {
				log.Warn("scrape", "url", u, "err", err)
				continue
			}
			all = append(all, s...)
		}
		fresh := map[string]float64{}
		for name, sel := range cfg.Metrics {
			if v, ok := sel.Eval(all); ok {
				fresh[name] = v
			}
		}
		mu.Lock()
		maps.Copy(values, fresh)
		mu.Unlock()

		if ns != nil {
			for _, n := range cfg.OPCUA.Nodes {
				if v, ok := fresh[n.Metric]; ok {
					ns.SetValue(n.Node, v)
				}
			}
		}
		if mc != nil {
			publish(mc, buf, cfg, fresh, time.Now(), log)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// publish sends this round's readings; while the broker cannot be reached they wait in buf,
// and once it can, what waited goes first (in order, with the time each was taken).
func publish(mc paho.Client, buf *Buffer, cfg Config, fresh map[string]float64, now time.Time, log *slog.Logger) {
	// IsConnected is also true while paho reconnects; only an open connection takes a publish
	if !mc.IsConnectionOpen() {
		for _, p := range cfg.MQTT.Publish {
			if v, ok := fresh[p.Metric]; ok {
				if err := buf.Add(p.Topic, v, now); err != nil {
					log.Error("mqtt buffer", "err", err)
				}
			}
		}
		return
	}
	if buf.Len() > 0 {
		n, err := buf.Drain(func(topic string, payload []byte) error {
			tok := mc.Publish(topic, 1, false, payload)
			if !tok.WaitTimeout(5 * time.Second) {
				return fmt.Errorf("broker did not acknowledge")
			}
			return tok.Error()
		})
		log.Info("mqtt buffer: sent what waited", "readings", n, "left", buf.Len(), "dropped", buf.Dropped(), "err", err)
		if err != nil {
			for _, p := range cfg.MQTT.Publish {
				if v, ok := fresh[p.Metric]; ok {
					buf.Add(p.Topic, v, now)
				}
			}
			return
		}
	}
	for _, p := range cfg.MQTT.Publish {
		if v, ok := fresh[p.Metric]; ok {
			mc.Publish(p.Topic, 0, false, strconv.FormatFloat(v, 'f', 2, 64))
		}
	}
}

// registers serves the latest measurements as holding registers and remembers coil writes,
// which is where operator commands from the twin land.
type registers struct {
	get   func(string) (float64, bool)
	cfg   Config
	mu    sync.Mutex
	coils map[uint16]bool
	log   *slog.Logger
}

func (r *registers) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if req.IsWrite {
		r.log.Info("modbus register write", "addr", req.Addr, "values", req.Args)
		return req.Args, nil
	}
	out := make([]uint16, req.Quantity)
	for i := range out {
		addr := req.Addr + uint16(i)
		for _, h := range r.cfg.Modbus.Holding {
			if h.Address != addr {
				continue
			}
			if v, ok := r.get(h.Metric); ok {
				scale := h.Scale
				if scale == 0 {
					scale = 1
				}
				out[i] = uint16(int16(math.Round(v * scale)))
			}
		}
	}
	return out, nil
}

func (r *registers) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	return r.HandleHoldingRegisters(&modbus.HoldingRegistersRequest{Addr: req.Addr, Quantity: req.Quantity, UnitId: req.UnitId})
}

func (r *registers) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.IsWrite {
		for i, v := range req.Args {
			r.coils[req.Addr+uint16(i)] = v
		}
		r.log.Info("modbus coil write", "addr", req.Addr, "values", req.Args)
		return req.Args, nil
	}
	out := make([]bool, req.Quantity)
	for i := range out {
		out[i] = r.coils[req.Addr+uint16(i)]
	}
	return out, nil
}

func (r *registers) HandleDiscreteInputs(req *modbus.DiscreteInputsRequest) ([]bool, error) {
	return make([]bool, req.Quantity), nil
}
