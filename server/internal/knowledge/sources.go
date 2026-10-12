package knowledge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"smartfactory/internal/anomaly"
	"smartfactory/internal/audit"
	"smartfactory/internal/calib"
	"smartfactory/internal/checks"
	"smartfactory/internal/guard"
)

// Sources are where what happens to the machines comes from; any of them may be nil.
type Sources struct {
	Checks *checks.Service
	Hints  *anomaly.Detector
	Calib  *calib.Service
	Guard  *guard.Guard
	// Audit has the commands AI sent that did not wait for a person.
	Audit *audit.Log
}

// Watch reads what happens to the machines from src and writes it again a few seconds after
// each of them says something changed. RunEvents does the writing.
func (b *Base) Watch(src Sources) {
	also := func(prev func(string)) func(string) {
		return func(c string) {
			if prev != nil {
				prev(c)
			}
			b.EventsChanged(c)
		}
	}
	if s := src.Checks; s != nil {
		s.Changed = also(s.Changed)
		b.Checks = func(company string, since time.Time) ([]CheckInfo, error) {
			list, err := s.Submissions(company, checks.Filter{From: since, Bad: true, Limit: perKind * 2})
			out := make([]CheckInfo, 0, len(list))
			for _, sub := range list {
				c := CheckInfo{ID: sub.ID, FormName: sub.FormName, Kind: checks.Kinds[sub.Kind], Plant: sub.Plant, Station: sub.Asset, Taken: sub.Taken, By: sub.By, Note: sub.Note}
				for _, v := range sub.Values {
					c.Values = append(c.Values, CheckValue{Label: v.Label, Text: v.Text, Num: v.Num, Unit: v.Unit, Bad: v.Bad, Why: v.Why, Photo: v.Kind == checks.Photo})
				}
				out = append(out, c)
			}
			return out, err
		}
		b.Rounds = func(company string) ([]RoundInfo, error) {
			list, err := s.Rounds(company)
			out := make([]RoundInfo, 0, len(list))
			for _, r := range list {
				out = append(out, RoundInfo{FormName: r.FormName, Kind: checks.Kinds[r.Kind], Plant: r.Plant, Station: r.Asset, Status: r.Status,
					Since: r.Since, Last: r.Last, LastBy: r.LastBy, LastBad: r.LastBad})
			}
			return out, err
		}
	}
	if d := src.Hints; d != nil {
		d.Changed = also(d.Changed)
		b.Anomalies = func(company string, since time.Time) ([]AnomalyInfo, error) {
			// the store keeps the hints of the last 30 days, each as it began and as it ended
			list, err := d.Recent(company, 500)
			out := make([]AnomalyInfo, 0, len(list))
			for _, a := range list {
				out = append(out, AnomalyInfo{ID: a.ID, Plant: a.Plant, Station: a.Asset, Field: anomaly.FieldLabel(a.Field), State: anomaly.StateLabel(a.State),
					Kind: a.Kind, Since: a.Since, Ended: a.Ended, Value: a.Value, Z: a.Z, Peak: a.Peak, Mean: a.Mean, Sigma: a.Sigma})
			}
			return out, err
		}
	}
	if s := src.Calib; s != nil {
		s.Changed = also(s.Changed)
		b.Calibrations = func(company string) ([]CalibInfo, error) {
			list, err := s.Calibrations(company)
			out := make([]CalibInfo, 0, len(list))
			for _, m := range list {
				c := CalibInfo{Plant: m.Plant, Station: m.Asset, FittedAt: m.FittedAt, From: m.From, To: m.To, AppliedAt: m.AppliedAt, AppliedBy: m.AppliedBy}
				for _, p := range m.Params {
					c.Params = append(c.Params, CalibParam{Label: p.Label, Unit: p.Unit, Template: p.Template, Fitted: p.Fitted, Applied: p.Applied})
				}
				for _, r := range m.Replay {
					c.Replay = append(c.Replay, CalibReplay{Field: anomaly.FieldLabel(r.Field), Before: r.Before, After: r.After})
				}
				out = append(out, c)
			}
			return out, err
		}
	}
	if g := src.Guard; g != nil {
		g.Changed = also(g.Changed)
		trail := src.Audit
		b.Commands = func(company string, since time.Time) ([]CommandInfo, error) {
			reqs, err := g.Between(company, since, time.Now().Add(time.Minute))
			if err != nil {
				return nil, err
			}
			var out []CommandInfo
			for _, r := range reqs {
				out = append(out, CommandInfo{Key: r.ID, Request: r.ID, Plant: r.Plant, Station: r.Asset, Cmd: guard.Text(r.Cmd, r.Arg), Who: r.Who, Via: r.Via,
					At: r.Created, Status: r.Status, Reason: r.Reason, Context: r.Context, Result: r.Result, DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt})
			}
			// what an MCP client sent that went (or was refused) straight away; the assistants
			// send no commands
			es, err := trail.Find(context.Background(), company, since, "mcp", []string{"command", "command.refused"}, perKind*2)
			for _, e := range es {
				plant, asset, _ := strings.Cut(e.Target, "/")
				c := CommandInfo{Key: fmt.Sprint("log-", e.ID), Plant: plant, Station: asset, Cmd: e.Detail, Who: e.Who, Via: e.Via,
					At: e.At, Status: "sent", Result: e.Error}
				switch {
				case e.Action == "command.refused":
					c.Status = "refused"
				case !e.OK:
					c.Status = "failed"
				}
				// the log's context is the machine as it was, and the rule that refused it
				c.Context, _, _ = strings.Cut(e.Context, "；规则：")
				if _, rule, ok := strings.Cut(e.Context, "；规则："); ok {
					c.Result += "（规则：" + rule + "）"
				}
				out = append(out, c)
			}
			return out, err
		}
	}
}
