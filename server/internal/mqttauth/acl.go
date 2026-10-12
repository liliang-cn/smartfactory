// Package mqttauth gives each device on the embedded broker an identity of its own — a token, or
// a client certificate from the server's small CA — and the topics that identity may use: a
// device publishes readings for its own assets only, and hears only their commands. A revoked
// device is cut off at once.
package mqttauth

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Scope is where a device belongs: one company's plant, and the assets it speaks for.
type Scope struct {
	Company string `json:"company"`
	Plant   string `json:"plant"`
	// Assets are asset ids; "*" is every asset of the plant, and a trailing * a prefix ("RF-*").
	Assets []string `json:"assets"`
}

// Covers reports whether the device speaks for asset.
func (s Scope) Covers(asset string) bool {
	if asset == "" {
		return false
	}
	for _, p := range s.Assets {
		switch {
		case p == "*":
			return true
		case strings.HasSuffix(p, "*"):
			if strings.HasPrefix(asset, strings.TrimSuffix(p, "*")) {
				return true
			}
		case p == asset:
			return true
		}
	}
	return false
}

// Everything: the device covers every asset of its plant.
func (s Scope) Everything() bool {
	for _, p := range s.Assets {
		if p == "*" {
			return true
		}
	}
	return false
}

// MayPublish: a reading of one of its assets under the convention, {prefix}/{company}/{plant}/{asset}
// or …/{asset}/{field} — never a command topic.
func (s Scope) MayPublish(prefix, topic string) bool {
	p := strings.Split(topic, "/")
	if len(p) != 4 && len(p) != 5 {
		return false
	}
	if p[0] != prefix || p[1] != s.Company || p[2] != s.Plant || !plain(p[3]) {
		return false
	}
	if len(p) == 5 && (p[4] == "cmd" || !plain(p[4])) {
		return false
	}
	return s.Covers(p[3])
}

// MayRead: the command topic of one of its assets, {prefix}/{company}/{plant}/{asset}/cmd, or
// with + for the asset when it covers the whole plant. The broker asks this both of the filter a
// device subscribes with and of each topic it would deliver.
func (s Scope) MayRead(prefix, filter string) bool {
	p := strings.Split(filter, "/")
	if len(p) != 5 || p[0] != prefix || p[1] != s.Company || p[2] != s.Plant || p[4] != "cmd" {
		return false
	}
	if p[3] == "+" {
		return s.Everything()
	}
	return plain(p[3]) && s.Covers(p[3])
}

// plain is a topic level with no wildcards.
func plain(seg string) bool { return seg != "" && !strings.ContainsAny(seg, "+#") }

var (
	nameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	assetRe = regexp.MustCompile(`^(\*|[^/+#*\s]{1,64}\*?)$`)
	// a topic level: no separators or wildcards
	idRe = regexp.MustCompile(`^[^/+#*\s]{1,64}$`)
)

// CheckName: a device's name is its MQTT client id and user name, and its certificate's CN.
func CheckName(name string) error {
	if !nameRe.MatchString(name) {
		return errors.New("设备名用字母、数字、点、横线、下划线，最长 64 个字符，以字母或数字开头，比如 rf-line1")
	}
	return nil
}

// Check is what a scope must be.
func (s Scope) Check() error {
	if !idRe.MatchString(s.Company) {
		return errors.New("要有公司")
	}
	if !idRe.MatchString(s.Plant) {
		return errors.New("选一个厂")
	}
	if len(s.Assets) == 0 {
		return errors.New("至少写一台设备，比如 HP-101，或 RF-* 表示 RF- 开头的，* 表示整个厂")
	}
	if len(s.Assets) > 200 {
		return errors.New("设备最多 200 项，多的用 RF-* 这样的前缀")
	}
	for _, a := range s.Assets {
		if !assetRe.MatchString(a) {
			return fmt.Errorf("「%s」不是设备编号或前缀（如 HP-101、RF-*、*）", a)
		}
	}
	return nil
}
