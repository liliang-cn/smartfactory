package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/ops"
	"smartfactory/internal/stores"
	"smartfactory/internal/tags"
)

// A factory's BOMs through the API the console uses: members read, operators write; a BOM typed
// in and one imported from a CSV sheet go in effect, the second revision with a change record;
// an order the ERP sends is exploded against the stores and comes out short of what they lack;
// every change is in the activity log.
func TestBOMAPI(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	eng.Single()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	orders, err := erp.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { orders.Close() })
	prod := erp.New(orders, eng, log)
	sto, err := stores.Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sto.Close() })
	sto.Categories = func(string) []stores.Category {
		return []stores.Category{{Key: "bar", Label: "棒料", Kind: stores.Bar, Unit: "t", Fields: []string{"牌号", "规格"}}, {Key: "part", Label: "标准件", Kind: stores.Part, Unit: "件"}}
	}

	admin, adk := auth.NewToken("厂长", auth.Admin, nil, "")
	op, opk := auth.NewToken("技术部 陈工", auth.Operator, []string{"jianghe"}, "")
	member, mk := auth.NewToken("计划员", auth.Member, []string{"jianghe"}, "")
	ts := httptest.NewServer((&Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", adk, opk, mk), Audit: trail, Log: log, Production: prod, Stores: sto}).Handler())
	t.Cleanup(ts.Close)
	do := func(token, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	var cfg map[string]any
	json.Unmarshal([]byte(eng.BuiltinTemplate("huaduan")), &cfg)
	cfg["id"], cfg["name"], cfg["short"] = "jianghe", "江河锻造", "江河"
	if got, b := do(admin, "POST", "/api/admin/companies", cfg); got != 200 {
		t.Fatalf("build: %d %s", got, b)
	}

	if got, _ := do("", "GET", "/api/bom/jianghe", nil); got != 401 {
		t.Errorf("a visitor reads the BOMs: %d", got)
	}
	got, b := do(member, "GET", "/api/bom/jianghe", nil)
	var ov ops.BOMOverview
	json.Unmarshal(b, &ov)
	if got != 200 || ov.Operator || len(ov.Kinds) != 5 || len(ov.Parts) != 0 {
		t.Fatalf("overview: %d %s", got, b)
	}
	crank := stores.BOM{Part: "QZ-B20-4", Name: "2.0T 四缸曲轴", Drawing: "B", Lines: []stores.BOMLine{
		{ID: "F", Code: "QZ-B20-4-DJ", Name: "曲轴锻件", Kind: stores.Make, Qty: 1, Op: "热模锻"},
		{Parent: "F", Code: "GB-42CrMo-80", Name: "42CrMo 圆钢", Grade: "42CrMo", Spec: "φ80", Kind: stores.Raw, Qty: 27.4, Unit: "kg", Scrap: 2, Op: "下料"},
		{Code: "BZ-PLUG-M10", Name: "油道螺塞", Kind: stores.Std, Qty: 2, Op: "装配"},
	}}
	if got, _ := do(member, "POST", "/api/bom/jianghe", crank); got != 403 {
		t.Errorf("a member wrote a BOM: %d", got)
	}
	got, b = do(op, "POST", "/api/bom/jianghe", crank)
	var a stores.BOM
	json.Unmarshal(b, &a)
	if got != 200 || a.Rev != "A" || a.Status != stores.Draft {
		t.Fatalf("save: %d %s", got, b)
	}
	bad := crank
	bad.Lines = []stores.BOMLine{{Name: "螺塞", Qty: -1}}
	if got, b := do(op, "POST", "/api/bom/jianghe", bad); got != 422 || !strings.Contains(string(b), "用量要大于 0") {
		t.Errorf("a bad BOM: %d %s", got, b)
	}
	if got, b := do(op, "POST", "/api/bom/jianghe/"+a.ID+"/release", stores.Release{}); got != 200 {
		t.Fatalf("release A: %d %s", got, b)
	}

	// revision B from a CSV sheet, three plugs
	sheet := "层级,物料编码,物料名称,规格,材质,单件用量,单位,损耗率,工序\n1,QZ-B20-4-DJ,曲轴锻件,,,1,件,,热模锻\n2,GB-42CrMo-80,42CrMo 圆钢,φ80,42CrMo,27.4,kg,2%,下料\n1,BZ-PLUG-M10,油道螺塞,M10×1,,3,件,,装配\n"
	got, b = do(op, "POST", "/api/bom/jianghe/import", ops.BOMImport{Part: "QZ-B20-4", Name: "2.0T 四缸曲轴", Drawing: "C", File: "曲轴BOM.csv", Data: []byte(sheet)})
	var im ops.BOMImported
	json.Unmarshal(b, &im)
	if got != 200 || im.BOM == nil || im.BOM.Rev != "B" || im.Lines != 3 || im.BOM.Lines[1].Parent != im.BOM.Lines[0].ID {
		t.Fatalf("import: %d %s", got, b)
	}
	if got, b := do(op, "POST", "/api/bom/jianghe/import", ops.BOMImport{Part: "X", File: "a.xls", Data: []byte("x")}); got != 422 || !strings.Contains(string(b), "另存为 .xlsx") {
		t.Errorf("an old Excel file: %d %s", got, b)
	}
	got, b = do(op, "POST", "/api/bom/jianghe/"+im.BOM.ID+"/release", stores.Release{Why: "客户图纸 C 版加一条油道"})
	var rel ops.BOMReleased
	json.Unmarshal(b, &rel)
	if got != 200 || rel.ECO == nil || rel.ECO.From != "A" || !strings.Contains(strings.Join(rel.ECO.Changes, "；"), "用量 2 件 → 3 件") {
		t.Fatalf("release B: %d %s", got, b)
	}
	got, b = do(member, "GET", "/api/bom/jianghe/parts/QZ-B20-4", nil)
	var part ops.BOMPart
	json.Unmarshal(b, &part)
	if got != 200 || len(part.Revisions) != 2 || part.Revisions[0].Status != stores.Retired || len(part.ECOs) != 2 {
		t.Fatalf("part: %d %s", got, b)
	}

	// the stores: 3 t of the bar, 1000 plugs
	_, b = do(op, "POST", "/api/stores/jianghe/items", stores.Item{Cat: "bar", Code: "GB-42CrMo-80", Name: "42CrMo 圆钢 φ80", Attrs: map[string]string{"牌号": "42CrMo", "规格": "φ80"}})
	var bar stores.Item
	json.Unmarshal(b, &bar)
	do(op, "POST", "/api/stores/jianghe/moves", stores.MoveInput{Item: bar.ID, Kind: stores.In, Qty: 3, Heat: "26A1", Mill: "华东特钢"})
	_, b = do(op, "POST", "/api/stores/jianghe/items", stores.Item{Cat: "part", Code: "BZ-PLUG-M10", Name: "油道螺塞"})
	var plug stores.Item
	json.Unmarshal(b, &plug)
	do(op, "POST", "/api/stores/jianghe/moves", stores.MoveInput{Item: plug.ID, Kind: stores.In, Qty: 1000})

	got, b = do(member, "GET", "/api/bom/jianghe/explode?part=QZ-B20-4&qty=100", nil)
	var x stores.Explosion
	json.Unmarshal(b, &x)
	if got != 200 || x.Rev != "B" || len(x.Needs) != 2 || x.Short != 0 || x.Needs[1].StoreUnit != "t" {
		t.Fatalf("explode 100: %d %s", got, b)
	}

	// an order of 500: 13.97 t of bar against 3 t, 1500 plugs against 1000
	if got, b := do(op, "POST", "/api/erp/jianghe/orders", map[string]any{"no": "MO-1", "partNo": "QZ-B20-4", "qty": 500}); got != 200 || !strings.Contains(strings.ReplaceAll(string(b), " ", ""), `"ok":true`) {
		t.Fatalf("order: %d %s", got, b)
	}
	got, b = do(member, "GET", "/api/bom/jianghe/kitting", nil)
	var k stores.Kitting
	json.Unmarshal(b, &k)
	if got != 200 || len(k.Orders) != 1 || k.Orders[0].Ready || k.Orders[0].Short != 2 || len(k.Short) != 2 {
		t.Fatalf("kitting: %d %s", got, b)
	}
	for _, s := range k.Short {
		if s.Code == "BZ-PLUG-M10" && s.Short != 500 || s.Code == "GB-42CrMo-80" && (s.Short < 10970 || s.Short > 10975) {
			t.Errorf("short: %+v", s)
		}
	}
	got, b = do(member, "GET", "/api/bom/jianghe/where?q=螺塞", nil)
	if got != 200 || !strings.Contains(string(b), "QZ-B20-4") {
		t.Errorf("where used: %d %s", got, b)
	}

	es, _ := trail.List(context.Background(), "jianghe", 0, 100)
	var acts []string
	for _, e := range es {
		acts = append(acts, e.Action)
	}
	all := strings.Join(acts, " ")
	for _, want := range []string{"bom.save", "bom.release", "bom.import"} {
		if !strings.Contains(all, want) {
			t.Errorf("activity log lacks %s: %s", want, all)
		}
	}
}
