package mqttauth

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestScope(t *testing.T) {
	s := Scope{Company: "huaduan", Plant: "p1", Assets: []string{"RF-*", "HP-101"}}
	for _, c := range []struct {
		topic string
		pub   bool
		read  bool
	}{
		{"sf/huaduan/p1/HP-101", true, false},
		{"sf/huaduan/p1/HP-101/temp", true, false},
		{"sf/huaduan/p1/RF-07/temp", true, false},
		{"sf/huaduan/p1/RF-/temp", true, false},
		{"sf/huaduan/p1/HP-102/temp", false, false},
		{"sf/huaduan/p1/HP-1010/temp", false, false},
		// commands go one way: to the device
		{"sf/huaduan/p1/HP-101/cmd", false, true},
		{"sf/huaduan/p1/RF-07/cmd", false, true},
		{"sf/huaduan/p1/HP-102/cmd", false, false},
		// another plant, company or prefix
		{"sf/huaduan/p2/HP-101/temp", false, false},
		{"sf/jianghe/p1/HP-101/temp", false, false},
		{"xx/huaduan/p1/HP-101/temp", false, false},
		// not the convention
		{"sf/huaduan/p1", false, false},
		{"sf/huaduan/p1/HP-101/temp/x", false, false},
		{"sf/huaduan/p1/HP-101/", false, false},
		{"$SYS/broker/clients", false, false},
		// wildcards: only + for the asset, and only for a device that covers the plant
		{"sf/huaduan/p1/+/cmd", false, false},
		{"sf/huaduan/p1/#", false, false},
		{"sf/huaduan/+/HP-101/cmd", false, false},
		{"#", false, false},
		{"sf/huaduan/p1/RF-+/cmd", false, false},
	} {
		if got := s.MayPublish("sf", c.topic); got != c.pub {
			t.Errorf("publish %s = %v, want %v", c.topic, got, c.pub)
		}
		if got := s.MayRead("sf", c.topic); got != c.read {
			t.Errorf("read %s = %v, want %v", c.topic, got, c.read)
		}
	}
	all := Scope{Company: "huaduan", Plant: "p1", Assets: []string{"*"}}
	if !all.MayRead("sf", "sf/huaduan/p1/+/cmd") || !all.MayPublish("sf", "sf/huaduan/p1/anything/temp") {
		t.Error("a device with * covers the whole plant")
	}
	if all.MayRead("sf", "sf/huaduan/p1/#") || all.MayPublish("sf", "sf/huaduan/p1/x/cmd") {
		t.Error("even a whole-plant device reads only commands and publishes no commands")
	}
}

func TestCheck(t *testing.T) {
	ok := Scope{Company: "huaduan", Plant: "p1", Assets: []string{"RF-*", "HP-101", "*", "炉-1"}}
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Scope{
		{Company: "huaduan", Plant: "p1"},
		{Company: "huaduan", Plant: "", Assets: []string{"*"}},
		{Company: "hua/duan", Plant: "p1", Assets: []string{"*"}},
		{Company: "huaduan", Plant: "p1", Assets: []string{"HP-+"}},
		{Company: "huaduan", Plant: "p1", Assets: []string{"*HP"}},
		{Company: "huaduan", Plant: "p1", Assets: []string{"HP 1"}},
		{Company: "huaduan", Plant: "p1", Assets: []string{"a/b"}},
	} {
		if bad.Check() == nil {
			t.Errorf("%+v passed", bad)
		}
	}
	for _, n := range []string{"", "-x", "a b", "a/b", "设备"} {
		if CheckName(n) == nil {
			t.Errorf("name %q passed", n)
		}
	}
}

// A token works for its device only and only until it is renewed or the device revoked; a
// certificate is the one in force, issued by the CA kept (0600) in the data directory.
func TestRegistry(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Reserve("device")
	var dropped []string
	drops := make(chan string, 4)
	r.Watch(func() {}, func(n string) { drops <- n })
	sc := Scope{Company: "huaduan", Plant: "p1", Assets: []string{"RF-*"}}
	if _, _, err := r.Add("厂长", "device", Token, sc); err == nil {
		t.Fatal("a shared user's name was taken")
	}
	d, cred, err := r.Add("厂长", "rf-line1", Token, sc)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Token == "" || cred.Cert != "" {
		t.Fatal("a token device got no token, or a certificate")
	}
	if _, _, err := r.Add("厂长", "rf-line1", Token, sc); err == nil {
		t.Fatal("two devices with one name")
	}
	if _, _, ok := r.Login("rf-line1", "wrong"); ok {
		t.Fatal("a wrong token logged in")
	}
	got, sum, ok := r.Login("rf-line1", cred.Token)
	if !ok || got.ID != d.ID {
		t.Fatal("the device's own token did not log in")
	}
	if _, ok := r.Current("rf-line1", sum); !ok {
		t.Fatal("the credential is not current")
	}
	for _, f := range []string{"devices.db", "devices.db-wal"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		if bytes.Contains(b, []byte(cred.Token)) {
			t.Fatal("the token itself is stored")
		}
	}

	// renewed: the old token stops, the connection on it is dropped
	_, cred2, err := r.Renew("huaduan", d.ID)
	if err != nil {
		t.Fatal(err)
	}
	dropped = append(dropped, <-drops)
	if _, _, ok := r.Login("rf-line1", cred.Token); ok {
		t.Fatal("the old token works after renewal")
	}
	if _, ok := r.Current("rf-line1", sum); ok {
		t.Fatal("a session on the old token is still current")
	}
	if _, _, ok := r.Login("rf-line1", cred2.Token); !ok {
		t.Fatal("the new token does not work")
	}
	if _, _, err := r.Renew("jianghe", d.ID); err != ErrNotFound {
		t.Fatal("another company renewed it")
	}

	// a certificate device
	if _, err := os.Stat(filepath.Join(dir, "ca")); err == nil {
		t.Fatal("the CA was made before it was needed")
	}
	cd, cc, err := r.Add("厂长", "hp-plc", Cert, Scope{Company: "huaduan", Plant: "p1", Assets: []string{"HP-101"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ca", "ca/ca.key", "ca/ca.crt"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if want := os.FileMode(0o600); st.IsDir() {
			if st.Mode().Perm() != 0o700 {
				t.Errorf("%s is %v", f, st.Mode().Perm())
			}
		} else if st.Mode().Perm() != want {
			t.Errorf("%s is %v", f, st.Mode().Perm())
		}
	}
	pair, err := tls.X509KeyPair([]byte(cc.Cert), []byte(cc.Key))
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	pool := x509.NewCertPool()
	blk, _ := pem.Decode([]byte(cc.CA))
	caCert, _ := x509.ParseCertificate(blk.Bytes)
	pool.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	if leaf.Subject.CommonName != "hp-plc" || len(leaf.URIs) != 1 || leaf.URIs[0].String() != "urn:smartfactory:device:hp-plc" {
		t.Fatalf("the certificate names %q %v", leaf.Subject.CommonName, leaf.URIs)
	}
	if _, _, ok := r.LoginCert(leaf); !ok {
		t.Fatal("the device's certificate did not log in")
	}
	if _, _, ok := r.Login("hp-plc", ""); ok {
		t.Fatal("a certificate device logged in with no password")
	}

	// it survives a restart, CA included
	r.Seen("rf-line1")
	r.Refuse("rf-line1", "发 sf/huaduan/p1/HP-101/temp")
	r.Close()
	r, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.Watch(func() {}, func(n string) { drops <- n })
	if _, _, ok := r.LoginCert(leaf); !ok {
		t.Fatal("the certificate does not work after a restart")
	}
	list, _ := r.List("huaduan")
	if len(list) != 2 || list[1].Name != "rf-line1" || list[1].Refused != 1 || list[1].LastSeen == nil {
		t.Fatalf("listed %v", names(list))
	}

	// revoked: refused at once, and listed as revoked
	if _, err := r.Revoke("huaduan", cd.ID, "厂长"); err != nil {
		t.Fatal(err)
	}
	dropped = append(dropped, <-drops)
	if _, _, ok := r.LoginCert(leaf); ok {
		t.Fatal("a revoked device's certificate works")
	}
	list, _ = r.List("huaduan")
	if len(list) != 2 || list[1].Revoked == nil || list[1].Name != "hp-plc" {
		t.Fatalf("listed %v", names(list))
	}
	// its name can be used again
	if _, _, err := r.Add("厂长", "hp-plc", Token, sc); err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 2 || dropped[0] != "rf-line1" || dropped[1] != "hp-plc" {
		t.Fatalf("dropped %v", dropped)
	}
}

func names(list []Device) []string {
	var out []string
	for _, d := range list {
		out = append(out, fmt.Sprintf("%s refused=%d revoked=%v", d.Name, d.Refused, d.Revoked != nil))
	}
	return out
}
