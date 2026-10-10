package transcode

import "testing"

func TestGatePausesForPlayback(t *testing.T) {
	playing := true
	g := NewGate(func() bool { return playing }, func() *float64 { return nil }, 75)
	if g.Reason() == "" {
		t.Fatal("expected pause during playback")
	}
	playing = false
	if r := g.Reason(); r != "" {
		t.Fatalf("expected clear, got %q", r)
	}
}

func TestGateTemperatureHysteresis(t *testing.T) {
	temp := 60.0
	g := NewGate(func() bool { return false }, func() *float64 { return &temp }, 75)
	for _, tc := range []struct {
		temp   float64
		paused bool
	}{{60, false}, {75, true}, {72, true}, {70.1, true}, {70, false}, {74, false}} {
		temp = tc.temp
		if got := g.Reason() != ""; got != tc.paused {
			t.Errorf("at %.1fC paused=%v, want %v", tc.temp, got, tc.paused)
		}
	}
}

func TestGateUnreadableTemperatureAndDisabled(t *testing.T) {
	g := NewGate(nil, func() *float64 { return nil }, 75)
	if g.Reason() != "" {
		t.Fatal("unreadable temperature must not pause")
	}
	hot := 99.0
	if NewGate(nil, func() *float64 { return &hot }, 0).Reason() != "" {
		t.Fatal("pauseAbove=0 must disable the check")
	}
	var nilGate *Gate
	if nilGate.Reason() != "" {
		t.Fatal("nil gate must never pause")
	}
}
