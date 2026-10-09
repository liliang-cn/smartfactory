package agents

import (
	"context"
	"strings"
	"testing"

	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"

	"smartfactory/internal/auth"
)

func TestChunkFollowsHeadings(t *testing.T) {
	ps := chunk("# 指导书\n\n前言\n\n## 1 加热\n\n出炉 1180～1230 ℃\n\n### 1.1 测温\n\n红外逐件\n\n## 2 锻造\n\n首件三检\n")
	var got []string
	for _, p := range ps {
		got = append(got, p.heading+"|"+p.text)
	}
	want := []string{"指导书|前言", "指导书 › 1 加热|出炉 1180～1230 ℃", "指导书 › 1 加热 › 1.1 测温|红外逐件", "指导书 › 2 锻造|首件三检"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("chunks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	long := chunk(strings.Repeat("一段话。\n\n", 400))
	for _, p := range long {
		if n := len([]rune(p.text)); n > 900 {
			t.Fatalf("a part of %d characters", n)
		}
	}
}

// Chinese has no spaces: a query phrased differently from the text still finds it.
func TestRankChinese(t *testing.T) {
	items := []string{"模具预热至 200～300 ℃，模温低于 150 ℃ 不得开锻", "离合器制动角超标停机", "大头厚度 22.0 mm"}
	got := rank(items, "开锻前模具温度", 3, func(s string) string { return s })
	if len(got) == 0 || got[0] != items[0] {
		t.Fatalf("got %q", got)
	}
	if got := rank(items, "HP-501", 3, func(s string) string { return s }); len(got) != 0 {
		t.Fatalf("nothing matches HP-501, got %q", got)
	}
}

func TestSeedAndWriteGate(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("AGENTGO_HOME", t.TempDir())
	r, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for range 2 {
		if err := r.Ensure("huaduan"); err != nil {
			t.Fatal(err)
		}
	}
	as, _ := r.Store.Agents("huaduan")
	ds, _ := r.Store.Docs("huaduan")
	ns, _ := r.Store.Notes("huaduan")
	tk, _ := r.Store.Tickets("huaduan")
	if len(as) != 3 || len(ds) != 5 || len(ns) != 3 || len(tk) != 3 {
		t.Fatalf("seeded %d assistants, %d documents, %d notes, %d tickets", len(as), len(ds), len(ns), len(tk))
	}
	// a company with no material of its own gets one assistant of each kind
	r.Ensure("tiankong")
	if as, _ := r.Store.Agents("tiankong"); len(as) != len(Templates) {
		t.Fatalf("tiankong has %d assistants", len(as))
	}
	if ds, _ := r.Store.Docs("tiankong"); len(ds) != 0 {
		t.Fatal("tiankong got huaduan's documents")
	}

	// writes go through agent-go's permission gate: members and above of this company only
	allow := func(w auth.Who) bool {
		res, err := permit("huaduan")(auth.With(context.Background(), w), agentpkg.PermissionRequest{ToolName: "create_ticket"})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Allowed && !res.ContinueRun {
			t.Fatal("a refusal must leave the run going, so the model can hand over a draft")
		}
		return res.Allowed
	}
	for _, c := range []struct {
		who  auth.Who
		want bool
	}{
		{auth.Anonymous, false},
		{auth.Who{Name: "张工", Role: auth.Member, Companies: []string{"huaduan"}}, true},
		{auth.Who{Name: "外厂", Role: auth.Admin, Companies: []string{"tiankong"}}, false},
		{auth.Who{Name: "管理员", Role: auth.Admin}, true},
	} {
		if got := allow(c.who); got != c.want {
			t.Errorf("%s (%s, %v) may write: %v, want %v", c.who.Name, c.who.Role, c.who.Companies, got, c.want)
		}
	}

	ts := tools(r.Store, nil, "huaduan")
	in := args{"customer": "远岳动力", "product": "连杆 LG-B20", "title": "厚度超差", "detail": "5 件", "severity": "高"}
	got, err := ts["create_ticket"].run(auth.With(context.Background(), auth.Who{Name: "张工", Role: auth.Member}), in)
	if err != nil {
		t.Fatal(err)
	}
	if tk := got.(Ticket); !strings.HasSuffix(tk.ID, "-0004") || tk.Log[0].By != "张工" {
		t.Fatalf("new ticket %s by %s after three", tk.ID, tk.Log[0].By)
	}
	hits, _ := ts["search_docs"].run(context.Background(), args{"query": "模具 预热"})
	if ps := hits.([]Part); len(ps) == 0 || !strings.Contains(ps[0].Text, "200～300") {
		t.Fatalf("search_docs: %v", hits)
	}
}
