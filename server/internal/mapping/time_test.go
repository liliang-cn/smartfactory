package mapping

import (
	"strconv"
	"testing"
	"time"

	"smartfactory/internal/tags"
)

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 10, 11, 8, 30, 0, 0, time.UTC)
	for _, v := range []any{
		float64(want.UnixMilli()), float64(want.Unix()), float64(want.UnixMicro()),
		itoa(want.UnixMilli()), "2026-10-11T08:30:00Z", "2026-10-11T16:30:00+08:00",
	} {
		got, ok := ParseTime(v)
		if !ok || !got.Equal(want) {
			t.Errorf("%v: %v %v", v, got, ok)
		}
	}
	for _, v := range []any{nil, "soon", -5.0, true} {
		if _, ok := ParseTime(v); ok {
			t.Errorf("%v read as a time", v)
		}
	}
}

// Readings carry the device's own time when the payload gives one; a time in the future is
// replaced by arrival time and flagged; payloads without one keep working.
func TestConventionTimestamps(t *testing.T) {
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	took := now.Add(-3 * time.Hour)
	ms := func(t time.Time) string { return time.Time.Format(t, time.RFC3339Nano) }
	cases := []struct {
		topic, payload string
		at             time.Time
		q              uint8
		n              int
	}{
		{"sf/c/p1/HP-101/temp", "61.5", time.Time{}, 0, 1},
		{"sf/c/p1/HP-101/temp", `{"v": 61.5, "t": ` + itoa(took.UnixMilli()) + `}`, took, 0, 1},
		{"sf/c/p1/HP-101/temp", `{"value": 61.5, "t": "` + ms(took) + `"}`, took, 0, 1},
		{"sf/c/p1/HP-101/temp", `{"v": 61.5, "t": ` + itoa(now.Add(time.Hour).UnixMilli()) + `}`, now, tags.FlagClock, 1},
		{"sf/c/p1/HP-101", `{"temp": 61.5, "power": 230, "t": ` + itoa(took.Unix()) + `}`, took, 0, 2},
		{"sf/c/p1/HP-101", `{"temp": {"v": 61.5, "t": ` + itoa(took.UnixMilli()) + `}}`, took, 0, 1},
	}
	for _, c := range cases {
		ts, err := ConventionAt("sf", c.topic, []byte(c.payload), "dev", now)
		if err != nil || len(ts) != c.n {
			t.Fatalf("%s %s: %v %v", c.topic, c.payload, ts, err)
		}
		for _, tg := range ts {
			if !tg.At.Equal(c.at) || tg.Q != c.q {
				t.Errorf("%s: %s at %v flags %d, want %v %d", c.payload, tg.Field, tg.At, tg.Q, c.at, c.q)
			}
			if f, ok := tg.Value.(float64); !ok || (tg.Field == "temp" && f != 61.5) {
				t.Errorf("%s: value %v", c.payload, tg.Value)
			}
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
