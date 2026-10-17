package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"smartfactory/internal/client"
	"smartfactory/internal/knowledge"
)

// cliOntology: a company's ontology — its classes, relations, rules and constraints; the things of
// a class; one thing with why each inferred fact holds; a SPARQL query.
func cliOntology(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ontology", flag.ExitOnError)
	n := fs.Int("n", 100, "things")
	rest := flags(fs, args)
	if len(rest) == 0 {
		return errors.New(`smartfactory ontology <company> [instances <类> [关系=值] | explain <名字> | sparql "SELECT …"]`)
	}
	c, err := connect()
	if err != nil {
		return err
	}
	company := client.Q(rest[0])
	verb := ""
	if len(rest) > 1 {
		verb = rest[1]
	}
	arg := strings.Join(rest[min(2, len(rest)):], " ")
	var raw json.RawMessage
	switch verb {
	case "":
		if err := c.Do(ctx, "GET", "/api/knowledge/"+company+"/ontology", nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var o knowledge.OntOverview
		json.Unmarshal(raw, &o)
		if !o.Declared {
			fmt.Println(dim("这家公司的行业包没有声明本体，只有内置的类和关系。"))
		} else {
			fmt.Println(dim(fmt.Sprintf("给定的事实 %d 条，推理出 %d 条", o.Explicit, o.Inferred)))
		}
		fmt.Println(bold("\n类"))
		for _, cl := range o.Classes {
			if cl.Builtin && cl.Asserted+cl.Inferred == 0 && len(cl.Sub) == 0 {
				continue
			}
			sub := ""
			if len(cl.Sub) > 0 {
				sub = " ⊂ " + strings.Join(cl.Sub, "、")
			}
			count := fmt.Sprint(cl.Asserted)
			if cl.Inferred > 0 {
				count += fmt.Sprintf(" + 推理 %d", cl.Inferred)
			}
			fmt.Printf("  %s%s  %s %s\n", cl.Name, dim(sub), count, dim(cl.Note))
		}
		fmt.Println(bold("\n关系"))
		for _, r := range o.Relations {
			if !r.Builtin {
				fmt.Printf("  %s %s\n", r.Text, dim(r.Note))
			}
		}
		if len(o.Rules) > 0 {
			fmt.Println(bold("\n规则"))
			for _, r := range o.Rules {
				fmt.Printf("  %s：%s %s\n", r.ID, r.Text, dim(r.Note))
			}
		}
		if len(o.Constraints) > 0 {
			fmt.Println(bold("\n约束"))
			for _, k := range o.Constraints {
				state := green("✓")
				if k.Status == "violated" {
					state = red("✗")
				}
				what := k.Text
				if k.Live {
					what += dim(fmt.Sprintf("（看着 %d 台）", k.Watching))
				}
				fmt.Printf("  %s %s：%s\n", state, k.ID, what)
				for _, v := range k.Violations {
					since := ""
					if v.Since > 0 {
						since = dim(" " + time.UnixMilli(v.Since).Format("01-02 15:04:05") + " 起")
					}
					fmt.Printf("      %s%s\n", red(v.Text), since)
				}
			}
		}
	case "instances":
		class, filter, _ := strings.Cut(arg, " ")
		rel, value, _ := strings.Cut(strings.TrimSpace(filter), "=")
		if class == "" {
			return errors.New("smartfactory ontology <company> instances <类> [关系=值]")
		}
		path := fmt.Sprintf("/api/knowledge/%s/ontology/instances?class=%s&relation=%s&value=%s&limit=%d", company, urlQ(class), urlQ(rel), urlQ(value), *n)
		if err := c.Do(ctx, "GET", path, nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var in knowledge.OntInstances
		json.Unmarshal(raw, &in)
		fmt.Println(dim(fmt.Sprintf("%s：%d 个", class, in.Total)))
		for _, x := range in.Instances {
			tag := ""
			if x.Inferred {
				tag = blue("推理")
			}
			if x.Fact != nil && x.Fact.Inferred {
				tag = blue(x.Fact.Relation + "是推理出的")
			}
			fmt.Printf("  %s %s %s\n", x.Name, dim(kindLabel[x.Kind]), tag)
		}
	case "explain":
		if arg == "" {
			return errors.New("smartfactory ontology <company> explain <名字>")
		}
		if err := c.Do(ctx, "GET", fmt.Sprintf("/api/knowledge/%s/ontology/thing?name=%s", company, urlQ(arg)), nil, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var t knowledge.OntThing
		json.Unmarshal(raw, &t)
		kind := cmpOr(kindLabel[t.Kind], map[string]string{"class": "类", "thing": "东西"}[t.Kind])
		fmt.Printf("%s %s %s\n", bold(t.Name), dim(kind), dim(strings.Join(t.Also, "、")))
		if t.Note != "" {
			fmt.Println("  " + t.Note)
		}
		facts := func(title string, fs []knowledge.OntFact) {
			if len(fs) == 0 {
				return
			}
			fmt.Println(bold("\n" + title))
			for _, f := range fs {
				line := fmt.Sprintf("%s %s %s", f.Subject, f.Relation, f.Object)
				if !f.Inferred {
					fmt.Println("  " + line)
					continue
				}
				fmt.Printf("  %s %s\n", line, blue("推理"))
				for _, w := range f.Why {
					fmt.Println(dim("      " + w))
				}
			}
		}
		facts("是", t.Classes)
		facts("是一种", t.Super)
		facts("事实", t.Facts)
		facts("关于它", t.Back)
		if t.Kind == "class" {
			if len(t.Subs) > 0 {
				fmt.Printf("%s\n  %s\n", bold("\n下级类"), strings.Join(t.Subs, "、"))
			}
			for _, r := range t.Relations {
				fmt.Println("  " + dim(r))
			}
			for _, k := range t.Constraints {
				fmt.Println("  约束 " + k)
			}
			if t.Instances != nil {
				var names []string
				for _, x := range t.Instances.Instances {
					names = append(names, x.Name)
				}
				fmt.Printf("%s\n  %s\n", bold(fmt.Sprintf("\n%d 个", t.Instances.Total)), strings.Join(names, "、"))
			}
		}
	case "sparql":
		if arg == "" {
			return errors.New(`smartfactory ontology <company> sparql "SELECT ?x WHERE { ?x a c:菜品 }"`)
		}
		if err := c.Do(ctx, "POST", "/api/knowledge/"+company+"/ontology/sparql", map[string]string{"query": arg}, &raw); err != nil {
			return err
		}
		if asJSON {
			return printJSON(raw)
		}
		var a knowledge.SPARQLAnswer
		json.Unmarshal(raw, &a)
		if a.Boolean != nil {
			fmt.Println(*a.Boolean)
			return nil
		}
		rows := make([][]string, len(a.Rows))
		for i, r := range a.Rows {
			for _, v := range a.Vars {
				rows[i] = append(rows[i], r[v])
			}
		}
		table(a.Vars, rows)
		if a.Cut {
			fmt.Println(dim(fmt.Sprintf("只列了前 %d 行", len(a.Rows))))
		}
	default:
		return fmt.Errorf("ontology %s：用 instances、explain、sparql", verb)
	}
	return nil
}
