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
