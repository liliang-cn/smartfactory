package mqttauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// CertLife is how long a device's certificate is good for; a new one can be issued any time.
const CertLife = 3 * 365 * 24 * time.Hour

// CA is the server's own certificate authority, made the first time it is needed and kept in
// {data}/ca (only the server's user can read it). It signs the devices' client certificates and,
// when none is configured, the broker's TLS certificate. Devices trust ca.crt.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

// LoadCA reads the CA in dir, or makes it.
func LoadCA(dir string) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	cb, cerr := os.ReadFile(certPath)
	kb, kerr := os.ReadFile(keyPath)
	if errors.Is(cerr, os.ErrNotExist) && errors.Is(kerr, os.ErrNotExist) {
		return newCA(dir, certPath, keyPath)
	}
	if cerr != nil {
		return nil, cerr
	}
	if kerr != nil {
		return nil, kerr
	}
	cert, err := parseCert(cb)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", certPath, err)
	}
	key, err := parseKey(kb)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyPath, err)
	}
	return &CA{cert: cert, key: key, pem: cb}, nil
}

func newCA(dir, certPath, keyPath string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "SmartFactory device CA " + host, Organization: []string{"SmartFactory"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(20, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM, err := keyPEM(key)
	if err != nil {
		return nil, err
	}
	// the key first: a certificate without its key is no use, the other way round it is made again
	if err := writePrivate(keyPath, keyPEM); err != nil {
		return nil, err
	}
	if err := writePrivate(certPath, certPEM); err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, pem: certPEM}, nil
}

// PEM is the CA's certificate, which devices trust.
func (ca *CA) PEM() []byte { return ca.pem }

// Pool is the CA as the TLS listener checks client certificates against it.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// Issued is a device's new certificate and its private key, which the server does not keep.
type Issued struct {
	Cert, Key []byte
	Serial    string
	Expires   time.Time
}

// IssueDevice makes a client certificate naming the device: CN is its name, and a URI SAN
// urn:smartfactory:device:{name}.
func (ca *CA) IssueDevice(name string) (*Issued, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sn := serial()
	tpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: name, Organization: []string{"SmartFactory device"}},
		URIs:         []*url.URL{{Scheme: "urn", Opaque: "smartfactory:device:" + name}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(CertLife),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	kp, err := keyPEM(key)
	if err != nil {
		return nil, err
	}
	return &Issued{Cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), Key: kp, Serial: hex.EncodeToString(sn.Bytes()), Expires: tpl.NotAfter}, nil
}

// ServerCert is a certificate for the broker's TLS listener, for these host names and
// addresses. It is made at each start and not kept: devices trust the CA, not this.
func (ca *CA) ServerCert(hosts []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "SmartFactory MQTT broker", Organization: []string{"SmartFactory"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else if h != "" {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key}, nil
}

// Hosts are the names and addresses this machine is reached by: its host name, localhost and
// every interface's address, plus those given.
func Hosts(extra ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, h := range extra {
		add(h)
	}
	if h, err := os.Hostname(); err == nil {
		add(h)
	}
	add("localhost")
	add("127.0.0.1")
	add("::1")
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && !n.IP.IsLinkLocalUnicast() {
				add(n.IP.String())
			}
		}
	}
	return out
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n.Add(n, big.NewInt(1))
}

func keyPEM(k *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func parseCert(b []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func parseKey(b []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("not a PEM key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an ECDSA key")
	}
	return ek, nil
}

// writePrivate writes a file only the server's user can read, whole or not at all.
func writePrivate(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
