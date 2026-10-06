package mapping

import (
	"reflect"
	"testing"

	"smartfactory/internal/config"
)

func TestMatchTopic(t *testing.T) {
	cases := []struct {
		filter, topic string
		caps          []string
		ok            bool
	}{
		{"plant/+/press/+/temp", "plant/a/press/3/temp", []string{"a", "3"}, true},
		{"plant/+/temp", "plant/a/b/temp", nil, false},
		{"plant/#", "plant/a/b", []string{"a/b"}, true},
		{"a/b", "a/b", nil, true},
		{"a/b", "a/b/c", nil, false},
	}
	for _, c := range cases {
		caps, ok := MatchTopic(c.filter, c.topic)
		if ok != c.ok || (ok && !reflect.DeepEqual(caps, c.caps)) {
			t.Errorf("MatchTopic(%q, %q) = %v %v, want %v %v", c.filter, c.topic, caps, ok, c.caps, c.ok)
		}
	}
	if got := Expand("HP-{1}0{2}", []string{"1", "3"}); got != "HP-103" {
		t.Errorf("Expand = %q", got)
	}
}

func TestExtract(t *testing.T) {
	for _, c := range []struct {
		payload, path string
		want          any
	}{
		{"61.5", "", 61.5},
		{"true", "", true},
		{"running", "", "running"},
		{`{"v": 12}`, "", 12.0},
		{`{"data": {"temp": [1, 2.5]}}`, "data.temp.1", 2.5},
	} {
		got, err := Extract([]byte(c.payload), c.path)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Extract(%q, %q) = %v, %v; want %v", c.payload, c.path, got, err, c.want)
		}
	}
}

func TestConvert(t *testing.T) {
	s, o := 0.1, -2.0
	if got := Convert(655.0, config.Point{Scale: &s, Offset: &o}); got != 63.5 {
		t.Errorf("scale/offset = %v", got)
	}
	if got := Convert(2.0, config.Point{Map: map[string]any{"2": "fault"}}); got != "fault" {
		t.Errorf("map = %v", got)
	}
}

func TestConvention(t *testing.T) {
	tg, err := Convention("sf", "sf/huaduan/p1/HP-101/temp", []byte("63.2"), "mqtt")
	if err != nil || len(tg) != 1 || tg[0].Asset != "HP-101" || tg[0].Value != 63.2 {
		t.Fatalf("single field: %+v %v", tg, err)
	}
	tg, err = Convention("sf", "sf/huaduan/p1/HP-101", []byte(`{"temp": 60, "power": 300}`), "mqtt")
	if err != nil || len(tg) != 2 {
		t.Fatalf("object: %+v %v", tg, err)
	}
	if tg, _ := Convention("sf", "sf/huaduan/p1/HP-101/cmd", []byte(`{}`), "mqtt"); tg != nil {
		t.Fatalf("cmd topic should be ignored: %+v", tg)
	}
}
