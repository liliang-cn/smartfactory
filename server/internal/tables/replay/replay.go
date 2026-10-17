// Package replay makes up a restaurant's day of table reports — lunch and dinner rushes, a
// late supper — and sends them to a server's report API as a camera box would: for tests, and
// to show a demo shop with reported tables.
package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"time"

	"smartfactory/internal/tables"
)

// Table is one table to report on.
type Table struct {
	Plant string `json:"plant"`
	ID    string `json:"id"`
	Seats int    `json:"seats"`
}

// rush is how busy each hour of the day is (0 none … 1 every table wanted).
var rush = map[int]float64{10: 0.05, 11: 0.55, 12: 1, 13: 0.7, 14: 0.25, 15: 0.1, 16: 0.15, 17: 0.55, 18: 1, 19: 1, 20: 0.75, 21: 0.45, 22: 0.4, 23: 0.35, 0: 0.2}

// Day is a day of reports for these tables from 10:00 to 01:00 of the day `day` falls in (local
// time), in time order: each group sits down (occupied, with how many), leaves (cleaning) and
// the table is cleared (free) a few minutes later. Bigger groups stay longer.
func Day(list []Table, day time.Time, source string, seed uint64) []tables.Report {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	start := tables.DayStart(day).Add(5 * time.Hour) // 10:00
	end := start.Add(15 * time.Hour)                 // 01:00
	var out []tables.Report
	for _, t := range list {
		at := start.Add(time.Duration(r.IntN(40)) * time.Minute)
		for at.Before(end) {
			h := at.In(start.Location()).Hour()
			if r.Float64() > rush[h] {
				at = at.Add(time.Duration(10+r.IntN(20)) * time.Minute)
				continue
			}
			seats := max(1, t.Seats)
			n := 1 + r.IntN(seats)
			stay := time.Duration(40+12*n+r.IntN(30)) * time.Minute
			left := at.Add(stay)
			if left.After(end) {
				left = end
			}
			clean := left.Add(time.Duration(3+r.IntN(5)) * time.Minute)
			out = append(out,
				tables.Report{Plant: t.Plant, Table: t.ID, State: tables.Occupied, People: n, Since: at.UnixMilli(), Source: source},
				tables.Report{Plant: t.Plant, Table: t.ID, State: tables.Cleaning, Since: left.UnixMilli(), Source: source},
				tables.Report{Plant: t.Plant, Table: t.ID, State: tables.Free, Since: clean.UnixMilli(), Source: source})
			at = clean.Add(time.Duration(r.IntN(15)) * time.Minute)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.(int64) < out[j].Since.(int64) })
	return out
}

// Until is the reports up to t (those a camera box would have sent by then).
func Until(list []tables.Report, t time.Time) []tables.Report {
	i := sort.Search(len(list), func(i int) bool { return list[i].Since.(int64) > t.UnixMilli() })
	return list[:i]
}

// Client sends reports to a server.
type Client struct {
	Base    string
	Company string
	// Token is a reporter token (or an operator's); it goes in the Authorization header only.
	Token string
	HTTP  *http.Client
}

// Send posts reports in batches of up to 200 and adds up what the server said.
func (c *Client) Send(ctx context.Context, list []tables.Report) (tables.Result, error) {
	var sum tables.Result
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	for i := 0; i < len(list); i += 200 {
		batch := list[i:min(len(list), i+200)]
		b, _ := json.Marshal(batch)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/report/"+url.PathEscape(c.Company)+"/tables", bytes.NewReader(b))
		if err != nil {
			return sum, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		res, err := hc.Do(req)
		if err != nil {
			return sum, err
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return sum, fmt.Errorf("%s: %s", res.Status, bytes.TrimSpace(body))
		}
		var r tables.Result
		if err := json.Unmarshal(body, &r); err != nil {
			return sum, err
		}
		sum.Accepted += r.Accepted
		sum.Late += r.Late
		sum.Duplicate += r.Duplicate
		sum.Unknown += r.Unknown
		sum.Invalid += r.Invalid
	}
	return sum, nil
}

// Tables reads a company's tables from a server.
func (c *Client) Tables(ctx context.Context) ([]Table, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/api/tables/"+url.PathEscape(c.Company), nil)
	if err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
		return nil, fmt.Errorf("%s: %s", res.Status, bytes.TrimSpace(b))
	}
	var ov struct {
		Tables []Table `json:"tables"`
	}
	if err := json.NewDecoder(res.Body).Decode(&ov); err != nil {
		return nil, err
	}
	return ov.Tables, nil
}
