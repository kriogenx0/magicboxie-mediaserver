package transcode

import (
	"testing"
	"time"
)

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

func TestGateResumesAfterStayingUnderTheLimit(t *testing.T) {
	temp := 76.0
	clock := time.Unix(0, 0)
	g := NewGate(func() bool { return false }, func() *float64 { return &temp }, 75)
	g.now = func() time.Time { return clock }
	if g.Reason() == "" {
		t.Fatal("not paused at 76°C")
	}
	temp = 73 // under the limit, but not 5 degrees under
	if g.Reason() == "" {
		t.Fatal("resumed as soon as it dipped under the limit")
	}
	clock = clock.Add(resumeAfterCool - time.Second)
	if g.Reason() == "" {
		t.Fatal("resumed before staying cool long enough")
	}
	clock = clock.Add(time.Second)
	if r := g.Reason(); r != "" {
		t.Fatalf("still paused after %v under the limit: %q", resumeAfterCool, r)
	}
	temp = 75
	if g.Reason() == "" {
		t.Fatal("not paused again at the limit")
	}
	temp = 74
	clock = clock.Add(time.Minute)
	g.Reason()
	temp = 76 // a spike restarts the cool-down
	g.Reason()
	temp = 74
	g.Reason()
	clock = clock.Add(resumeAfterCool - time.Second)
	if g.Reason() == "" {
		t.Fatal("a spike over the limit didn't restart the cool-down")
	}
}
