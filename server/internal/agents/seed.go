package agents

import (
	"embed"
	"encoding/json"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

//go:embed seed
var seedFS embed.FS

type seedFile struct {
	Agents []struct{ Kind, Name, Instructions string } `json:"agents"`
	Notes  []struct {
		Title, Body, By string
		Tags, Assets    []string
		At              time.Time
	} `json:"notes"`
	Tickets []struct {
		Customer, Product, Batch, Title, Detail, Severity, Status, Reply string
		Log                                                              []TicketLog
		At                                                               time.Time
	} `json:"tickets"`
}

// Ensure gives a company its first assistants the first time they are looked at: one of each
// template, and for companies that ship with material (seed/{company}), their documents, notes
// and tickets too.
func (r *Runner) Ensure(company string) error {
	return r.Store.seed(company, func() error {
		var sf seedFile
		if b, err := seedFS.ReadFile(path.Join("seed", company, "seed.json")); err == nil {
			if err := json.Unmarshal(b, &sf); err != nil {
				return err
			}
		}
		if len(sf.Agents) == 0 {
			for _, t := range Templates {
				sf.Agents = append(sf.Agents, struct{ Kind, Name, Instructions string }{t.Kind, t.Title, ""})
			}
		}
		for _, a := range sf.Agents {
			if _, err := r.Store.CreateAgent(company, a.Kind, a.Name, a.Instructions); err != nil {
				return err
			}
		}
		docs, _ := fs.Glob(seedFS, path.Join("seed", company, "docs", "*.md"))
		// the newest document is listed first: add them last to first
		slices.Reverse(docs)
		for _, d := range docs {
			b, _ := seedFS.ReadFile(d)
			title := strings.TrimSuffix(path.Base(d), ".md")
			if i := strings.IndexByte(title, '-'); i >= 0 {
				title = title[i+1:]
			}
			if _, err := r.Store.AddDoc(company, title, string(b), "资料室"); err != nil {
				return err
			}
		}
		for _, n := range sf.Notes {
			if _, err := r.Store.SaveNote(company, Note{Title: n.Title, Body: n.Body, Tags: n.Tags, Assets: n.Assets, By: n.By, Updated: n.At}); err != nil {
				return err
			}
		}
		for _, t := range sf.Tickets {
			upd := t.At
			if len(t.Log) > 0 {
				upd = t.Log[len(t.Log)-1].At
			}
			if _, err := r.Store.CreateTicket(company, Ticket{Customer: t.Customer, Product: t.Product, Batch: t.Batch, Title: t.Title, Detail: t.Detail,
				Severity: t.Severity, Status: t.Status, Reply: t.Reply, Log: t.Log, Created: t.At, Updated: upd}); err != nil {
				return err
			}
		}
		return nil
	})
}
