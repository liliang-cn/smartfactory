package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

	"smartfactory/internal/engine"
)

// A company's BOMs in its knowledge base: each part number with a revision in effect is a passage
// saying what goes into it, level by level, and the graph links the part to every component it
// uses (product —uses→ component) and to the product family it is a part number of, so "曲轴用到
// 哪些标准件" walks from the crankshaft to its keys and plugs, and "40Cr 棒料用在哪" back to it.

// BOMInfo is a part's BOM revision in effect, exploded for one of it.
type BOMInfo struct {
	Part, Name, Rev, Drawing, Effective string
	// Family is the product (family) name the part number belongs to ("" when none).
	Family string
	Lines  []BOMLineInfo
}

// BOMLineInfo is a line of it: how deep, what (Label: name with grade and spec), what kind
// (自制件、标准件 …), how much goes into one of the part, and at which operation.
type BOMLineInfo struct {
	Level             int
	Code, Label, Kind string
	Per               float64
	Unit, Op, Via     string
}

const bomFormat = "1"

type bomState struct {
	Items map[string]bomEntry `json:"items"`
}

type bomEntry struct {
	Hash  string   `json:"hash"`
	Name  string   `json:"name"`
	Parts []string `json:"parts,omitempty"`
}

func (b *Base) bomPath(id string) string { return filepath.Join(b.dir, id+".boms.json") }

func (b *Base) bomState(id string) bomState {
	st := bomState{Items: map[string]bomEntry{}}
	if raw, err := os.ReadFile(b.bomPath(id)); err == nil {
		json.Unmarshal(raw, &st)
	}
	if st.Items == nil {
		st.Items = map[string]bomEntry{}
	}
	return st
}

func bomID(part string) string { return "bom:" + part }

func perText(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// bomItem is a part's BOM as a passage, linked to its components and its family.
func bomItem(x BOMInfo) item {
	it := item{id: bomID(x.Part), title: "BOM " + x.Part + " " + x.Name, meta: map[string]string{"kind": "bom", "part": x.Part, "rev": x.Rev}}
	desc := "料号 " + x.Part
	it.ent(x.Name, TProduct, desc)
	if x.Family != "" && x.Family != x.Name {
		it.ent(x.Family, TProduct, "")
		it.rel(x.Name, RVariantOf, x.Family)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s（料号 %s）的物料清单（BOM）%s 版", x.Name, x.Part, x.Rev)
	if x.Drawing != "" {
		fmt.Fprintf(&b, "，按图纸 %s 版", x.Drawing)
	}
	if x.Effective != "" {
		fmt.Fprintf(&b, "，%s 起生效", x.Effective)
	}
	if x.Family != "" && x.Family != x.Name {
		fmt.Fprintf(&b, "，属于%s", x.Family)
	}
	b.WriteString("。每件用到：\n")
	byKind := map[string][]string{}
	var kinds []string
	for _, l := range x.Lines {
		fmt.Fprintf(&b, "%s- %s %s（%s）%s %s", strings.Repeat("  ", max(0, l.Level-1)), l.Kind, l.Label, l.Code, perText(l.Per), l.Unit)
		if l.Op != "" {
			fmt.Fprintf(&b, "，%s工序用", l.Op)
		}
		if l.Via != "" {
			fmt.Fprintf(&b, "，在%s里", l.Via)
		}
		b.WriteString("\n")
		it.ent(l.Label, TComponent, l.Kind+" "+l.Code)
		it.rel(x.Name, RUses, l.Label)
		if _, ok := byKind[l.Kind]; !ok {
			kinds = append(kinds, l.Kind)
		}
		if !contains(byKind[l.Kind], l.Label) {
			byKind[l.Kind] = append(byKind[l.Kind], l.Label)
		}
	}
	for _, k := range kinds {
		fmt.Fprintf(&b, "%s用到的%s：%s。\n", x.Name, k, strings.Join(byKind[k], "、"))
	}
	it.text = b.String()
	return it
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// SyncBOMs writes the company's BOMs in effect into its knowledge base: the new and changed
// ones, and removes those no longer in effect.
func (b *Base) SyncBOMs(ctx context.Context, id string) error {
	if b.BOMs == nil {
		return nil
	}
	list, err := b.BOMs(id)
	if err != nil {
		return err
	}
	st := b.bomState(id)
	next := bomState{Items: map[string]bomEntry{}}
	var write []item
	comps := map[string]bool{}
	for _, x := range list {
		it := bomItem(x)
		raw, _ := json.Marshal([]any{it.text, it.ents, it.rels})
		sum := sha256.Sum256(append(raw, bomFormat...))
		h := hex.EncodeToString(sum[:])
		var parts []string
		for _, e := range it.ents {
			if e.Type == TComponent {
				parts = append(parts, e.Name)
				comps[e.Name] = true
			}
		}
		next.Items[x.Part] = bomEntry{Hash: h, Name: x.Name, Parts: parts}
		if st.Items[x.Part].Hash != h {
			write = append(write, it)
		}
	}
	var gone []string
	for part := range st.Items {
		if _, ok := next.Items[part]; !ok {
			gone = append(gone, part)
		}
	}
	if len(write) == 0 && len(gone) == 0 {
		return nil
	}
	c, err := b.open(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, part := range gone {
		c.db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: bomID(part)})
	}
	for _, it := range write {
		if err := save(ctx, c.db, it); err != nil {
			return fmt.Errorf("%s: %w", it.id, err)
		}
	}
	prune(ctx, c.db, map[string]map[string]bool{TComponent: comps})
	out, _ := json.Marshal(next)
	return os.WriteFile(b.bomPath(id), out, 0o644)
}

// ForgetBOMs makes the next SyncBOMs write every BOM again.
func (b *Base) ForgetBOMs(id string) {
	if engine.ValidID.MatchString(id) {
		os.Remove(b.bomPath(id))
	}
}

// BOMsChanged asks for a company's BOMs to be written again soon.
func (b *Base) BOMsChanged(company string) {
	b.bmu.Lock()
	b.bdirty[company] = true
	b.bmu.Unlock()
	select {
	case b.bkick <- struct{}{}:
	default:
	}
}

// RunBOMs keeps every company's BOMs in its base: once at the start, and a few seconds after
// they change.
func (b *Base) RunBOMs(ctx context.Context) {
	for _, c := range b.eng.CompanyIDs() {
		b.BOMsChanged(c)
	}
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.bkick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		case <-tick.C:
			for _, c := range b.eng.CompanyIDs() {
				b.bmu.Lock()
				b.bdirty[c] = true
				b.bmu.Unlock()
			}
		}
		b.bmu.Lock()
		dirty := b.bdirty
		b.bdirty = map[string]bool{}
		b.bmu.Unlock()
		for c := range dirty {
			if err := b.SyncBOMs(ctx, c); err != nil {
				b.log.Error("knowledge: boms", "company", c, "err", err)
			}
		}
	}
}

// bomVocabulary are the names BOMs add to the graph: the part names and their components.
func (b *Base) bomVocabulary(id string) [][2]string {
	var out [][2]string
	for _, e := range b.bomState(id).Items {
		out = append(out, [2]string{e.Name, TProduct})
		for _, p := range e.Parts {
			out = append(out, [2]string{p, TComponent})
		}
	}
	return out
}
