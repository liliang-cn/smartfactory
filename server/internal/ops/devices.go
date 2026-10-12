package ops

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"smartfactory/internal/auth"
	"smartfactory/internal/mqttauth"
)

// A company's devices on the embedded broker: each with its own token or certificate and only
// its own assets' topics. Administrators register and revoke them.

// MQTT is the embedded broker as devices reach it.
type MQTT struct {
	Prefix string
	// Listen is where the plain listener is now (only this machine until a device can prove
	// who it is); TLS the TLS listener's address, "" for none.
	Listen func() string
	TLS    string
	// Shared are the shared user names of the config: whoever holds one may use any topic.
	Shared []string
}

func (o *Ops) devices(who auth.Who, company string) error {
	if o.Devices == nil {
		return fail(503, "这台服务器没有数据目录，设备只能用配置里的共用账号（mqtt.users）接入")
	}
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return fail(404, "没有公司 %s", company)
	}
	if !who.Can(company, auth.Admin) {
		if who.Key == "" {
			return fail(401, "用令牌登录")
		}
		return fail(403, "要公司 %s 的管理员令牌", company)
	}
	return nil
}

// BrokerView is the broker as the console shows it; Listen is empty when the server runs none.
type BrokerView struct {
	Listen string `json:"listen"`
	TLS    string `json:"tls,omitempty"`
	Prefix string `json:"prefix"`
	// Local: the broker takes connections from this machine only.
	Local bool `json:"local"`
}

// DevicesOverview is a company's registered devices, and the broker's shared users, which are
// not held to any topics.
type DevicesOverview struct {
	Devices []mqttauth.Device `json:"devices"`
	Shared  []string          `json:"shared"`
	Broker  BrokerView        `json:"broker"`
}

func (o *Ops) DeviceOverview(who auth.Who, company string) (*DevicesOverview, error) {
	if err := o.devices(who, company); err != nil {
		return nil, err
	}
	list, err := o.Devices.List(company)
	if err != nil {
		return nil, err
	}
	out := &DevicesOverview{Devices: list, Shared: []string{}}
	if o.MQTT != nil {
		out.Shared = append(out.Shared, o.MQTT.Shared...)
		sort.Strings(out.Shared)
		out.Broker = BrokerView{Listen: o.MQTT.Listen(), TLS: o.MQTT.TLS, Prefix: o.MQTT.Prefix}
		out.Broker.Local = strings.HasPrefix(out.Broker.Listen, "127.0.0.1:") || strings.HasPrefix(out.Broker.Listen, "localhost:") || strings.HasPrefix(out.Broker.Listen, "[::1]:")
	}
	return out, nil
}

// DeviceInput registers a device: its name, the plant and assets it speaks for, and the kind of
// credential it gets (token | cert).
type DeviceInput struct {
	Name   string   `json:"name"`
	Plant  string   `json:"plant"`
	Assets []string `json:"assets"`
	Kind   string   `json:"kind"`
}

// DeviceIssued is a device and its credential, shown this once: the token, or the certificate,
// its key and the CA (and the three as a zip, Bundle).
type DeviceIssued struct {
	Device mqttauth.Device `json:"device"`
	mqttauth.Credential
	Bundle []byte `json:"bundle,omitempty"`
}

func (o *Ops) AddDevice(who auth.Who, via, company string, in DeviceInput) (*DeviceIssued, error) {
	if err := o.devices(who, company); err != nil {
		return nil, err
	}
	var assets []string
	for _, a := range in.Assets {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(assets, a) {
			assets = append(assets, a)
		}
	}
	if in.Plant != "" && !o.hasPlant(company, in.Plant) {
		return nil, fail(422, "公司 %s 没有厂 %s", company, in.Plant)
	}
	if in.Kind == "" {
		in.Kind = mqttauth.Token
	}
	d, cred, err := o.Devices.Add(who.Name, in.Name, in.Kind, mqttauth.Scope{Company: company, Plant: in.Plant, Assets: assets})
	o.Audit.Record(who, via, company, "device.add", strings.TrimSpace(in.Name), deviceDetail(in.Plant, assets, in.Kind), err)
	if err != nil {
		return nil, deviceError(err)
	}
	return issued(d, cred)
}

// RenewDevice gives a device a new token or certificate; the old one stops working at once.
func (o *Ops) RenewDevice(who auth.Who, via, company, id string) (*DeviceIssued, error) {
	if err := o.devices(who, company); err != nil {
		return nil, err
	}
	d, cred, err := o.Devices.Renew(company, id)
	target := id
	if err == nil {
		target = d.Name
	}
	o.Audit.Record(who, via, company, "device.renew", target, kindLabel[d.Kind], err)
	if err != nil {
		return nil, deviceError(err)
	}
	return issued(d, cred)
}

// RevokeDevice stops a device at once: its connection is closed and its credential refused.
func (o *Ops) RevokeDevice(who auth.Who, via, company, id string) (*mqttauth.Device, error) {
	if err := o.devices(who, company); err != nil {
		return nil, err
	}
	d, err := o.Devices.Revoke(company, id, who.Name)
	target := id
	if err == nil {
		target = d.Name
	}
	o.Audit.Record(who, via, company, "device.revoke", target, deviceDetail(d.Plant, d.Assets, d.Kind), err)
	if err != nil {
		return nil, deviceError(err)
	}
	return &d, nil
}

var kindLabel = map[string]string{mqttauth.Token: "令牌", mqttauth.Cert: "证书"}

func deviceDetail(plant string, assets []string, kind string) string {
	if plant == "" {
		return kindLabel[kind]
	}
	return plant + "：" + strings.Join(assets, "、") + "，" + kindLabel[kind]
}

func deviceError(err error) error {
	var inv *mqttauth.Invalid
	switch {
	case errors.As(err, &inv):
		return fail(422, "%s", inv.Msg)
	case errors.Is(err, mqttauth.ErrNotFound):
		return fail(404, "没有这台设备，或它已经撤销了")
	}
	return err
}

func (o *Ops) hasPlant(company, plant string) bool {
	var list []struct {
		ID     string `json:"id"`
		Plants []struct {
			ID string `json:"id"`
		} `json:"plants"`
	}
	json.Unmarshal([]byte(o.Engine.List()), &list)
	for _, c := range list {
		if c.ID == company {
			for _, p := range c.Plants {
				if p.ID == plant {
					return true
				}
			}
		}
	}
	return false
}

func issued(d mqttauth.Device, cred *mqttauth.Credential) (*DeviceIssued, error) {
	out := &DeviceIssued{Device: d, Credential: *cred}
	if cred.Cert != "" {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		for _, f := range []struct{ name, body string }{{d.Name + ".crt", cred.Cert}, {d.Name + ".key", cred.Key}, {"ca.crt", cred.CA}} {
			w, err := z.Create(f.name)
			if err != nil {
				return nil, err
			}
			w.Write([]byte(f.body))
		}
		if err := z.Close(); err != nil {
			return nil, err
		}
		out.Bundle = buf.Bytes()
	}
	return out, nil
}
