package agents

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"smartfactory/internal/engine"
	"smartfactory/internal/knowledge"
)

// The assistants' search_docs finds a station's out-of-spec check from "RF-101 点检", and
// process_graph lists it among the station's relations, with what it was in a line.
func TestKnowledgeToolsSeeChecks(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	kb, err := knowledge.Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kb.Close)
	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	temp := 92.5
	kb.Checks = func(string, time.Time) ([]knowledge.CheckInfo, error) {
		return []knowledge.CheckInfo{{ID: "CK-1", FormName: "设备点检", Kind: "点检", Plant: "p1", Station: "RF-101", Taken: time.Now().Add(-time.Hour), By: "张工",
			Values: []knowledge.CheckValue{{Label: "温度表读数", Num: &temp, Unit: "°C", Bad: true, Why: "超上限 80"}}}}, nil
	}
	if err := kb.SyncEvents(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	ts := map[string]tool{}
	for _, x := range knowledgeTools(kb, "huaduan") {
		ts[x.name] = x
	}
	got, err := ts["search_docs"].run(ctx, args{"query": "RF-101 点检"})
	if err != nil {
		t.Fatal(err)
	}
	if a := got.(knowledge.Answer); len(a.Passages) == 0 || a.Passages[0].From != "check" || !strings.Contains(a.Passages[0].Text, "92.5 °C，超上限 80") {
		t.Errorf("search_docs: %+v", a)
	}
	got, err = ts["process_graph"].run(ctx, args{"name": "RF-101"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range got.(*knowledge.Entity).Links {
		found = found || l.Kind == "check" && l.Type == "checked_on" && strings.Contains(l.Title, "温度表读数")
	}
	if !found {
		t.Errorf("process_graph: %+v", got)
	}

	// ontology_query: a company without an ontology has the built-in classes, a station is a 工位
	got, err = ts["ontology_query"].run(ctx, args{})
	if o, _ := got.(*knowledge.OntOverview); err != nil || o == nil || o.Declared || len(o.Classes) == 0 {
		t.Errorf("ontology_query: %+v %v", got, err)
	}
	got, err = ts["ontology_query"].run(ctx, args{"name": "RF-101"})
	if th, _ := got.(*knowledge.OntThing); err != nil || th == nil || th.Kind != knowledge.TStation || th.Classes[0].Object != "工位" {
		t.Errorf("ontology_query RF-101: %+v %v", got, err)
	}
	got, err = ts["ontology_query"].run(ctx, args{"class": "工位"})
	if in, _ := got.(*knowledge.OntInstances); err != nil || in == nil || in.Total < 50 {
		t.Errorf("ontology_query 工位: %+v %v", got, err)
	}
}
