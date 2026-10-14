package ops

import (
	"context"
	"errors"
	"strings"

	"smartfactory/internal/auth"
	"smartfactory/internal/knowledge"
)

// A company's ontology, as its knowledge base holds it: members read it, administrators may also
// ask it in SPARQL.

func ontologyErr(err error) error {
	var e *knowledge.OntologyError
	if errors.As(err, &e) {
		if e.Missing {
			return fail(404, "%s", e.Msg)
		}
		return fail(400, "%s", e.Msg)
	}
	return err
}

// Ontology is the company's classes with how many things are of each, its relations, rules and
// constraints with where they stand now.
func (o *Ops) Ontology(ctx context.Context, who auth.Who, company string) (*knowledge.OntOverview, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	v, err := o.KB.OntologyOverview(ctx, company)
	return v, ontologyErr(err)
}

// OntologyInstances are the things of a class (its subclasses' too, and what inference says is
// of it); with relation and value, those that relate so to value.
func (o *Ops) OntologyInstances(ctx context.Context, who auth.Who, company, class, relation, value string, limit int) (*knowledge.OntInstances, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	class, relation, value = strings.TrimSpace(class), strings.TrimSpace(relation), strings.TrimSpace(value)
	if class == "" {
		return nil, fail(400, "哪个类")
	}
	if (relation == "") != (value == "") {
		return nil, fail(400, "按关系筛选要同时给关系和值")
	}
	v, err := o.KB.OntologyInstances(ctx, company, class, relation, value, max(1, min(limit, 500)))
	return v, ontologyErr(err)
}

// OntologyThing is a class or a thing with what the ontology says about it, each inferred fact
// with why.
func (o *Ops) OntologyThing(ctx context.Context, who auth.Who, company, name string) (*knowledge.OntThing, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fail(400, "查什么")
	}
	v, err := o.KB.OntologyThing(ctx, company, name)
	if err != nil {
		// a station as people write it: "fz101" is FZ-101
		if ls := o.KB.Resolve(company, name); len(ls) == 1 && ls[0].Name != name {
			if w, err2 := o.KB.OntologyThing(ctx, company, ls[0].Name); err2 == nil {
				return w, nil
			}
		}
	}
	return v, ontologyErr(err)
}

// OntologyGraph is the ontology's classes for drawing beside the knowledge graph.
func (o *Ops) OntologyGraph(ctx context.Context, who auth.Who, company string) (*knowledge.OntGraph, error) {
	if err := o.kb(who, company, auth.Member); err != nil {
		return nil, err
	}
	return o.KB.OntologyGraph(ctx, company)
}

// OntologySPARQL runs a read-only SPARQL query over the company's ontology; administrators only.
func (o *Ops) OntologySPARQL(ctx context.Context, who auth.Who, company, query string) (*knowledge.SPARQLAnswer, error) {
	if err := o.kb(who, company, auth.Admin); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, fail(400, "写一条 SPARQL")
	}
	v, err := o.KB.OntologySPARQL(ctx, company, query)
	return v, ontologyErr(err)
}
