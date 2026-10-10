package transcode

import (
	"fmt"
	"sync"
	"time"
)

// resumeMarginC is how far below the pause temperature the CPU must cool
// before work resumes right away, so it doesn't flap around the limit.
const resumeMarginC = 5.0

// resumeAfterCool resumes work once the CPU has stayed under the pause
// temperature this long, even if it never gets resumeMarginC cooler. A Pi
// in a warm spot can idle just under the limit, and transcoding must not
// stay off for good there.
const resumeAfterCool = 2 * time.Minute

// Gate decides whether transcoding should stand down: while something is
// being played back, or while the CPU is too hot.
type Gate struct {
	playing    func() bool
	temp       func() *float64
	pauseAbove float64 // degrees C; <= 0 disables the temperature check

	now func() time.Time

	mu       sync.Mutex
	hot      bool
	coolFrom time.Time // while hot: when the CPU dropped under pauseAbove
}

func NewGate(playing func() bool, temp func() *float64, pauseAboveC float64) *Gate {
	return &Gate{playing: playing, temp: temp, pauseAbove: pauseAboveC, now: time.Now}
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
			now := g.now()
			switch {
			case *t >= g.pauseAbove:
				g.hot = true
				g.coolFrom = time.Time{}
			case *t <= g.pauseAbove-resumeMarginC:
				g.hot = false
			case g.hot && g.coolFrom.IsZero():
				g.coolFrom = now
			case g.hot && now.Sub(g.coolFrom) >= resumeAfterCool:
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
