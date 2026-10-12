package audit

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"smartfactory/internal/auth"
)

// Pages run newest first and pick up where the last one ended; each company sees only its own.
func TestListPages(t *testing.T) {
	l, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	w := auth.Who{Name: "张工", Role: auth.Operator, Key: "abcd1234"}
	for i := range 5 {
		var e error
		if i == 2 {
			e = errors.New("找不到设备")
		}
		l.Record(w, "mcp", "huaduan", "command", "p1/HP-10"+string(rune('1'+i)), "stop", e)
	}
	l.Record(w, "page", "tiankong", "doc.add", "x", "", nil)

	first, err := l.List(context.Background(), "huaduan", 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || first[0].Target != "p1/HP-105" || first[2].Target != "p1/HP-103" {
		t.Fatalf("first page: %+v", first)
	}
	if first[2].OK || first[2].Error != "找不到设备" || first[0].Role != auth.Operator || first[0].Key != "abcd1234" {
		t.Errorf("entry kept wrong: %+v / %+v", first[2], first[0])
	}
	rest, _ := l.List(context.Background(), "huaduan", first[2].ID, 3)
	if len(rest) != 2 || rest[0].Target != "p1/HP-102" || rest[1].Target != "p1/HP-101" {
		t.Fatalf("second page: %+v", rest)
	}
	var none *Log
	none.Record(w, "mcp", "huaduan", "command", "", "", nil)
	if got, _ := none.List(context.Background(), "huaduan", 0, 10); len(got) != 0 {
		t.Error("a server without a data directory keeps nothing and lists nothing")
	}
}

// Counts add up a company's entries in a span by action, place and outcome; a prefix takes the
// actions under it and nothing that merely starts the same.
func TestCounts(t *testing.T) {
	l, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	w := auth.Who{Name: "张工", Role: auth.Operator}
	l.Record(w, "mcp", "huaduan", "command", "p1/HP-101", "stop", nil)
	l.Record(w, "mcp", "huaduan", "command", "p1/HP-102", "stop", nil)
	l.Record(w, "mcp", "huaduan", "command", "p1/HP-103", "stop", errors.New("找不到设备"))
	l.Record(w, "assistant", "huaduan", "command.refused", "p1/HP-101", "start", errors.New("温度太高"))
	l.Record(w, "page", "huaduan", "commands.export", "", "", nil)
	l.Record(w, "mcp", "tiankong", "command", "p1/HP-101", "stop", nil)
	ctx := context.Background()
	got, err := l.Counts(ctx, "huaduan", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "command")
	if err != nil {
		t.Fatal(err)
	}
	want := []Count{{"command", "mcp", false, 1}, {"command", "mcp", true, 2}, {"command.refused", "assistant", false, 1}}
	if !slices.Equal(got, want) {
		t.Errorf("counts: %+v, want %+v", got, want)
	}
	if got, _ := l.Counts(ctx, "huaduan", time.Now().Add(time.Hour), time.Now().Add(2*time.Hour), "command"); len(got) != 0 {
		t.Errorf("a span with nothing in it: %+v", got)
	}
	var none *Log
	if got, err := none.Counts(ctx, "huaduan", time.Time{}, time.Now(), "command"); err != nil || len(got) != 0 {
		t.Errorf("no log: %v %v", got, err)
	}
}
