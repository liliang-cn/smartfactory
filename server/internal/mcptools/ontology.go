package mcptools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
	"smartfactory/internal/knowledge"
)

type ontologyIn struct {
	Company string `json:"company" jsonschema:"company id"`
	Class   string `json:"class,omitempty" jsonschema:"a class: what it is a kind of, its subclasses, relations, constraints and things (leave out for the whole ontology)"`
}

type ontologyOut struct {
	Overview *knowledge.OntOverview `json:"overview,omitempty"`
	Class    *knowledge.OntThing    `json:"class,omitempty"`
	Note     string                 `json:"note"`
}

type ontologyInstancesIn struct {
	Company  string `json:"company" jsonschema:"company id"`
	Class    string `json:"class" jsonschema:"the class (Chinese name as the ontology has it, e.g. 菜品, 冷藏设备, 工位)"`
	Relation string `json:"relation,omitempty" jsonschema:"only things with this relation (e.g. 含过敏原) …"`
	Value    string `json:"value,omitempty" jsonschema:"… to this thing (e.g. 花生过敏原)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"at most this many (default 100, up to 500)"`
}

type ontologyExplainIn struct {
	Company  string `json:"company" jsonschema:"company id"`
	Name     string `json:"name" jsonschema:"a thing (a named thing of the ontology, a station such as FZ-101, a product, a machine) or a class"`
	Relation string `json:"relation,omitempty" jsonschema:"only its facts of this relation ('是' for its classes)"`
	Object   string `json:"object,omitempty" jsonschema:"only its facts about this thing or class"`
}

func addOntology(s *mcp.Server, kb Knowledge) {
	mcp.AddTool(s, &mcp.Tool{Name: "ontology", Description: "A company's ontology (本体): the kinds of things it declares (classes, with what each is a kind of and how many things are of it, given and inferred), " +
		"its relations (domain → range, transitive, symmetric, inverse), rules (property chains: X p Y, Y q Z → X r Z), and constraints with where they stand now: " +
		"live limits on a reading of every machine of a class (status violated, with the station and its value, while one is out of range) and structural ones (every thing of a class must have a relation; violations listed). " +
		"Built-in classes are the knowledge graph's kinds: 公司 工厂 产线 工位 设备 产品 供应商 客户 工单 and the machine records. With class: that class alone, with its things."},
		guard(auth.Member, func(in ontologyIn) string { return in.Company }, func(ctx context.Context, req *mcp.CallToolRequest, in ontologyIn) (*mcp.CallToolResult, ontologyOut, error) {
			if in.Class != "" {
				c, err := kb.OntologyThing(ctx, who(req), in.Company, in.Class)
				if err != nil {
					return nil, ontologyOut{}, err
				}
				if c.Kind != "class" {
					return nil, ontologyOut{}, fmt.Errorf("%s is a thing, not a class: ontology_explain explains it", in.Class)
				}
				return nil, ontologyOut{Class: c, Note: "super: what it is a kind of (inferred ones with why); instances: its things, those given first."}, nil
			}
			o, err := kb.Ontology(ctx, who(req), in.Company)
			if err != nil {
				return nil, ontologyOut{}, err
			}
			note := "ontology_instances lists the things of a class; ontology_explain shows a thing's facts and why an inferred one holds."
			if !o.Declared {
				note = "No ontology declared for this company's industry: only the built-in classes and relations."
			}
			return nil, ontologyOut{Overview: o, Note: note}, nil
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "ontology_instances", Description: "The things of a class of a company's ontology, with inference: things of its subclasses count (a 毛肚 is a 食材), " +
		"and things that are of it only because of a relation's domain or range. With relation and value, only the things that relate so to value, given or inferred " +
		"(菜品 with 含过敏原 = 花生过敏原 finds the dishes an ingredient carries the allergen into). Each says whether it is inferred, its kind in the knowledge graph (station, product …) if it is one, " +
		"and the matching fact. Given ones come first."},
		guard(auth.Member, func(in ontologyInstancesIn) string { return in.Company }, func(ctx context.Context, req *mcp.CallToolRequest, in ontologyInstancesIn) (*mcp.CallToolResult, knowledge.OntInstances, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 100
			}
			v, err := kb.OntologyInstances(ctx, who(req), in.Company, in.Class, in.Relation, in.Value, min(limit, 500))
			if err != nil {
				return nil, knowledge.OntInstances{}, err
			}
			return nil, *v, nil
		}))

	mcp.AddTool(s, &mcp.Tool{Name: "ontology_explain", Description: "One thing of a company's ontology (or a class) and everything the ontology says about it: its classes, its facts and the facts about it, " +
		"each given or inferred; an inferred one comes with why, a step a line from what was given (\"牛油锅底 原料 花生碎；花生碎 含过敏原 花生过敏原 → 牛油锅底 含过敏原 花生过敏原（规则「…」）\"). " +
		"A product's name or a station number works. Narrow it to one relation (and object) to explain one fact."},
		guard(auth.Member, func(in ontologyExplainIn) string { return in.Company }, func(ctx context.Context, req *mcp.CallToolRequest, in ontologyExplainIn) (*mcp.CallToolResult, knowledge.OntThing, error) {
			v, err := kb.OntologyThing(ctx, who(req), in.Company, in.Name)
			if err != nil {
				return nil, knowledge.OntThing{}, err
			}
			if in.Relation == "" && in.Object == "" {
				return nil, *v, nil
			}
			keep := func(fs []knowledge.OntFact) []knowledge.OntFact {
				out := []knowledge.OntFact{}
				for _, f := range fs {
					if (in.Relation == "" || f.Relation == in.Relation) && (in.Object == "" || f.Object == in.Object || f.Subject == in.Object) {
						out = append(out, f)
					}
				}
				return out
			}
			v.Classes, v.Facts, v.Back, v.Super = keep(v.Classes), keep(v.Facts), keep(v.Back), keep(v.Super)
			return nil, *v, nil
		}))
}
