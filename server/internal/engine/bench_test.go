package engine

import "testing"

func BenchmarkStep(b *testing.B) {
	e, err := New(discard())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		e.call("step", 0.1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.call("step", 0.1); err != nil {
			b.Fatal(err)
		}
	}
}

// One company per instance, the way the demo VM runs them: a 100 ms tick of the engine with
// nobody watching (0.5 s sim steps) and with a browser on it (0.1 s steps).
func BenchmarkCompanyTick(b *testing.B) {
	for _, c := range []string{"huaduan", "qianyu", "tiankong", "hetian"} {
		for _, watched := range []bool{false, true} {
			name := c + "/idle"
			w := ""
			if watched {
				name, w = c+"/watched", c
			}
			b.Run(name, func(b *testing.B) {
				e, err := New(discard())
				if err != nil {
					b.Fatal(err)
				}
				if err := e.Only([]string{c}); err != nil {
					b.Fatal(err)
				}
				if len(e.CompanyIDs()) != 1 {
					b.Skipf("no company %s", c)
				}
				// past the warm start, so the queues and orders a day builds up are there
				e.call("fastForward", c, 1800.0)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := e.call("step", 0.1, w); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
