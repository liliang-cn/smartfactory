// Package client is the command line's way to a SmartFactory server: the console's API with an
// administrator's token, the same calls the console page makes.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Login is where the command line talks to and as whom.
type Login struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "smartfactory", "cli.json"), nil
}

// Saved is the login kept by `smartfactory login`, with SF_SERVER and SF_TOKEN over it.
func Saved() (Login, error) {
	var l Login
	if p, err := path(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			json.Unmarshal(b, &l)
		}
	}
	if s := os.Getenv("SF_SERVER"); s != "" {
		l.Server = s
	}
	if t := os.Getenv("SF_TOKEN"); t != "" {
		l.Token = t
	}
	if l.Server == "" || l.Token == "" {
		return l, fmt.Errorf("还没登录：smartfactory login -server https://factory.superleo.cn（或设置 SF_SERVER、SF_TOKEN）")
	}
	l.Server = strings.TrimRight(l.Server, "/")
	return l, nil
}

// Save keeps l for later commands, readable only by this user.
func Save(l Login) (string, error) {
	p, err := path()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	return p, os.WriteFile(p, b, 0o600)
}

func Forget() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type Client struct {
	Login
	http *http.Client
}

func New(l Login) *Client { return &Client{Login: l, http: &http.Client{Timeout: 0}} }

// Error is what the server said when it refused.
type Error struct {
	Status   int
	Msg      string
	Problems []string
}

func (e *Error) Error() string {
	if len(e.Problems) == 0 {
		return e.Msg
	}
	return e.Msg + "\n  - " + strings.Join(e.Problems, "\n  - ")
}

func (c *Client) request(ctx context.Context, method, p string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Server+p, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-SF-Via", "cli")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		var e struct {
			Error  string   `json:"error"`
			Errors []string `json:"errors"`
		}
		b, _ := io.ReadAll(res.Body)
		if json.Unmarshal(b, &e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("%s（%d）", strings.TrimSpace(string(b)), res.StatusCode)
		}
		return nil, &Error{Status: res.StatusCode, Msg: e.Error, Problems: e.Errors}
	}
	return res, nil
}

// Do calls the API and decodes the answer into out (raw JSON when out is *json.RawMessage).
func (c *Client) Do(ctx context.Context, method, p string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := c.request(ctx, method, p, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Event is one server-sent event of the console agent or the scene architect.
type Event struct {
	Kind   string         `json:"kind"`
	ID     string         `json:"id"`
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	Result any            `json:"result"`
	Text   string         `json:"text"`
}

// Stream posts body and hands each event to on until the server closes.
func (c *Client) Stream(ctx context.Context, p string, body any, on func(Event)) error {
	res, err := c.request(ctx, "POST", p, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var e Event
		if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &e) == nil {
			on(e)
		}
	}
	return sc.Err()
}

// Q escapes a path segment.
func Q(s string) string { return url.PathEscape(s) }
