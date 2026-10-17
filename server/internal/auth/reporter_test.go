package auth

import "testing"

// A reporter token reports its companies' tables and is a visitor for everything else.
func TestReporter(t *testing.T) {
	r, err := ParseRole("reporter")
	if err != nil || r != Reporter || r.Label() != "桌态上报" || !r.Narrow() {
		t.Fatalf("parse: %v %v", r, err)
	}
	w := Who{Name: "前厅摄像头", Role: Reporter, Companies: []string{"qianyu"}, Key: "k"}
	if !w.Reports("qianyu") || w.Reports("huaduan") {
		t.Error("reports only its own company")
	}
	if w.Can("qianyu", Member) || w.Orders("qianyu", false) || w.RoleIn("qianyu") != Visitor {
		t.Error("a reporter is a visitor everywhere else")
	}
	op := Who{Name: "店长", Role: Operator, Companies: []string{"qianyu"}, Key: "o"}
	m := Who{Name: "店员", Role: Member, Companies: []string{"qianyu"}, Key: "m"}
	if !op.Reports("qianyu") || m.Reports("qianyu") {
		t.Error("operators report, members do not")
	}
}
