package transcode

import (
	"fmt"
	"sync"
)

// resumeMarginC is how far below the pause temperature the CPU must cool
// before work resumes, so it doesn't flap around the limit.
const resumeMarginC = 5.0

// Gate decides whether transcoding should stand down: while something is
// being played back, or while the CPU is too hot.
type Gate struct {
	playing    func() bool
	temp       func() *float64
	pauseAbove float64 // degrees C; <= 0 disables the temperature check

	mu  sync.Mutex
	hot bool
}

func NewGate(playing func() bool, temp func() *float64, pauseAboveC float64) *Gate {
	return &Gate{playing: playing, temp: temp, pauseAbove: pauseAboveC}
}

// Reason returns why transcoding should be paused, or "" if it may run.
// An unreadable temperature never pauses anything.
func (g *Gate) Reason() string {
	if g == nil {
		return ""
	}
	if g.playing != nil && g.playing() {
		return "playback in progress"
	}
	if g.pauseAbove > 0 && g.temp != nil {
		if t := g.temp(); t != nil {
			g.mu.Lock()
			if *t >= g.pauseAbove {
				g.hot = true
			} else if *t <= g.pauseAbove-resumeMarginC {
				g.hot = false
			}
			hot := g.hot
			g.mu.Unlock()
			if hot {
				return fmt.Sprintf("CPU at %.0f°C", *t)
			}
		}
	}
	return ""
}
