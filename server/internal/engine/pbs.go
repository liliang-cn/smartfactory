package engine

import (
	"encoding/json"
	"errors"
	"slices"
)

// The plant breakdown structure (PBS) of a company: company › plant › area (车间) › line › machine
// (by its tag, 位号), each node with how many machines are under it and in which state now.

// PBSNode is one node of the breakdown.
type PBSNode struct {
	// Kind is company, plant, area, line or machine.
	Kind string `json:"kind"`
	// ID is the plant's or line's id, the area's code, a machine's tag (HP-101).
	ID   string `json:"id"`
	Name string `json:"name"`
	// Sub says a little more: the plant's hall, the area's note, the line's product, the machine's kind.
	Sub string `json:"sub,omitempty"`
	// State and Fault are a machine's, now.
	State string `json:"state,omitempty"`
	Fault string `json:"fault,omitempty"`
	// Machines is how many machines are under the node (1 for a machine), States how many of them
	// are in each state (running, idle, blocked, stopped, fault, maint …).
	Machines int            `json:"machines"`
	Lines    int            `json:"lines"`
	States   map[string]int `json:"states"`
	Children []*PBSNode     `json:"children,omitempty"`
}

func (n *PBSNode) add(c *PBSNode) {
	n.Children = append(n.Children, c)
	n.Machines += c.Machines
	n.Lines += c.Lines
	for k, v := range c.States {
		n.States[k] += v
	}
}

type pbsArea struct {
	Code  string   `json:"code"`
	Name  string   `json:"name"`
	Note  string   `json:"note"`
	Lines []string `json:"lines"`
}

// pbsModel is the part of the process model (processModel) the breakdown is built from.
type pbsModel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Plants []struct {
		ID    string    `json:"id"`
		Name  string    `json:"name"`
		Hall  string    `json:"hall"`
		Areas []pbsArea `json:"areas"`
		Lines []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Product struct {
				Name string `json:"name"`
			} `json:"product"`
			Stations []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Label string `json:"label"`
			} `json:"stations"`
		} `json:"lines"`
	} `json:"plants"`
}

type pbsSnapshot struct {
	Plants []struct {
		ID     string `json:"id"`
		Assets []struct {
			ID    string `json:"id"`
			State string `json:"state"`
			Fault string `json:"fault"`
		} `json:"assets"`
	} `json:"plants"`
}

// ErrNoCompany is a breakdown asked of a company that does not run.
var ErrNoCompany = errors.New("no such company")

// PBS is the company's plant breakdown with each machine's state now.
func (e *Engine) PBS(company string) (*PBSNode, error) {
	raw := e.ProcessModel(company)
	if raw == "null" {
		return nil, ErrNoCompany
	}
	var m pbsModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	var snap pbsSnapshot
	json.Unmarshal([]byte(e.Snapshot(company)), &snap)
	return buildPBS(&m, &snap), nil
}

func buildPBS(m *pbsModel, snap *pbsSnapshot) *PBSNode {
	type state struct{ state, fault string }
	states := map[string]state{} // plant/asset → state
	for _, p := range snap.Plants {
		for _, a := range p.Assets {
			states[p.ID+"/"+a.ID] = state{a.State, a.Fault}
		}
	}
	node := func(kind, id, name, sub string) *PBSNode {
		return &PBSNode{Kind: kind, ID: id, Name: name, Sub: sub, States: map[string]int{}}
	}
	co := node("company", m.ID, m.Name, "")
	for _, p := range m.Plants {
		pn := node("plant", p.ID, p.Name, p.Hall)
		lines := map[string]*PBSNode{}
		order := []string{}
		for _, l := range p.Lines {
			ln := node("line", l.ID, l.Name, l.Product.Name)
			ln.Lines = 1
			for _, s := range l.Stations {
				st := states[p.ID+"/"+s.ID]
				mn := node("machine", s.ID, s.Name, s.Label)
				mn.State, mn.Fault, mn.Machines = st.state, st.fault, 1
				if st.state != "" {
					mn.States[st.state] = 1
				}
				ln.add(mn)
			}
			lines[l.ID] = ln
			order = append(order, l.ID)
		}
		areas := p.Areas
		if len(areas) == 0 {
			// a model from before areas: one, the hall
			areas = []pbsArea{{Code: "A1", Name: p.Hall, Lines: order}}
		}
		for i, a := range areas {
			an := node("area", a.Code, a.Name, a.Note)
			for _, id := range order {
				// a line no area names is in the first
				if slices.Contains(a.Lines, id) || i == 0 && !inAny(areas, id) {
					an.add(lines[id])
				}
			}
			pn.add(an)
		}
		co.add(pn)
	}
	return co
}

func inAny(areas []pbsArea, line string) bool {
	for _, a := range areas {
		if slices.Contains(a.Lines, line) {
			return true
		}
	}
	return false
}
