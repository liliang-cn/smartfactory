package knowledge

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"smartfactory/internal/engine"
)

// 华锻's process becomes a graph; a document that names a machine is found from a question that
// names only a station of that machine; the graph answers what comes before and after a station.
func TestProcessAndDocuments(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go eng.Run(ctx)
	kb, err := Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	defer kb.Close()

	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	st, err := kb.Stats(ctx, "huaduan")
	if err != nil {
		t.Fatal(err)
	}
	if st.Kinds[TStation] < 50 || st.Kinds[TLine] < 10 || st.Relations[RFeeds] < 40 || st.Process == 0 {
		t.Fatalf("stats: %+v", st)
	}

	e, err := kb.Lookup(ctx, "huaduan", "IH-101")
	if err != nil || e == nil {
		t.Fatalf("lookup: %v %v", e, err)
	}
	var feeds, isa, line bool
	for _, l := range e.Links {
		feeds = feeds || l.Type == RFeeds && l.Out
		isa = isa || l.Type == RIsA && l.Out && l.Kind == TMachine
		line = line || l.Type == RHasStation && !l.Out && l.Kind == TLine
	}
	if !feeds || !isa || !line {
		t.Errorf("IH-101 links: %+v", e.Links)
	}

	doc := []Part{{Seq: 0, Heading: "始锻温度", Text: "中频感应加热炉加热 42CrMo 坯料，始锻温度 1150～1200°C，终锻温度不低于 850°C。"}}
	if err := kb.AddDoc(ctx, "huaduan", "d1", "曲轴加热工艺卡", doc); err != nil {
		t.Fatal(err)
	}
	if !kb.HasDoc(ctx, "huaduan", "d1") {
		t.Error("the document is not in the base")
	}
	a, err := kb.Search(ctx, "huaduan", "IH-101 的始锻温度是多少", []string{"IH-101"}, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	found := slices.ContainsFunc(a.Passages, func(p Passage) bool { return p.From == "doc" && p.Doc == "d1" && strings.Contains(p.Text, "1150") })
	if !found {
		t.Errorf("the card was not found from the station (%s): %+v", a.Mode, a.Passages)
	}
	m, _ := kb.Lookup(ctx, "huaduan", "中频感应加热炉")
	if m == nil || len(m.Mentions) == 0 {
		t.Errorf("the machine does not know the card names it: %+v", m)
	}

	g, err := kb.Graph(ctx, "huaduan")
	if err != nil || len(g.Nodes) < 60 || len(g.Edges) < 60 {
		t.Fatalf("graph: %d nodes %d edges %v", len(g.Nodes), len(g.Edges), err)
	}

	// a name as people write it: "rf101" is RF-101, "SAW-101" is each plant's SAW-101
	if r := kb.Resolve("huaduan", "rf101"); len(r) != 1 || r[0].Name != "RF-101" || r[0].Kind != TStation {
		t.Errorf("rf101: %+v", r)
	}
	if r := kb.Resolve("huaduan", "saw 101"); len(r) < 2 || !slices.ContainsFunc(r, func(l Link) bool { return l.Name == "一厂 SAW-101" }) {
		t.Errorf("SAW-101 is in several plants: %+v", r)
	}
	if r := kb.Resolve("huaduan", "没有这台"); len(r) != 0 {
		t.Errorf("resolved nothing to %+v", r)
	}
	if got := kb.Recognize("huaduan", "ih101 出炉温度"); !slices.Contains(got, "IH-101") {
		t.Errorf("ih101 not recognised: %v", got)
	}
	ps, total, err := kb.Mentioning(ctx, "huaduan", "中频感应加热炉", 2)
	if err != nil || total < 3 || len(ps) != 2 || ps[0].From != "doc" || ps[0].Doc != "d1" || ps[0].Part != 0 || !strings.Contains(ps[0].Text, "1150") {
		t.Errorf("passages naming the furnace (%d, %v): %+v", total, err, ps)
	}

	// part of the graph: nearest first, as much as asked for
	sub, more, _ := g.Around("IH-101", 1, nil, 100, 100)
	names := map[string]bool{}
	for _, n := range sub.Nodes {
		names[n.Name] = true
	}
	if more != 0 || sub.Nodes[0].Name != "IH-101" || !names["RF-101"] || !names["中频感应加热炉"] || !names["曲轴 1 线"] || names["HP-101"] {
		t.Errorf("one hop around IH-101: %+v", sub.Nodes)
	}
	for _, e := range sub.Edges {
		if !names[e.From] || !names[e.To] {
			t.Errorf("an edge out of the part: %+v", e)
		}
	}
	sub, more, _ = g.Around("IH-101", 3, []string{TStation}, 3, 100)
	if len(sub.Nodes) != 3 || more == 0 || slices.ContainsFunc(sub.Nodes, func(n GraphNode) bool { return n.Kind != TStation }) {
		t.Errorf("stations around IH-101, three at most: %+v (%d more)", sub.Nodes, more)
	}
	sub, _, moreEdges := g.Around("", 0, []string{TLine, TStation}, 1000, 10)
	if len(sub.Edges) != 10 || moreEdges == 0 || slices.ContainsFunc(sub.Nodes, func(n GraphNode) bool { return n.Kind != TStation && n.Kind != TLine }) {
		t.Errorf("lines and stations: %d nodes, %d edges, %d more", len(sub.Nodes), len(sub.Edges), moreEdges)
	}

	kb.RemoveDoc(ctx, "huaduan", "d1", 1)
	if kb.HasDoc(ctx, "huaduan", "d1") {
		t.Error("a removed document is still there")
	}
	// unchanged process: nothing written again
	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
}
