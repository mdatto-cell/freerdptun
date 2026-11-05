package tunnel

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Shared token bucket. Counts caller-selected application bytes (IP or frame);
// 0 bits/s means unlimited.
// Refill credit survives scheduler oversleep: unlike sleeping once per packet,
// coarse timer resolution does not impose an unintended packets/s ceiling.
type Pacer struct {
	mu     sync.Mutex
	last   time.Time
	tokens float64
	bps    int64
	burst  int
}

const rateBurst = 64 * 1024

func NewPacer(bps int64) *Pacer { return newPacer(bps, rateBurst) }
func newPacer(bps int64, burst int) *Pacer {
	return &Pacer{bps: bps, burst: burst, last: time.Now(), tokens: float64(burst)}
}
func (p *Pacer) Wait(ctx context.Context, n int) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if p.bps == 0 {
		return nil
	}
	if n < 0 || n > p.burst {
		return errors.New("packet exceeds rate bucket capacity")
	}
	for {
		p.mu.Lock()
		now := time.Now()
		p.tokens += now.Sub(p.last).Seconds() * float64(p.bps) / 8
		p.last = now
		if p.tokens > float64(p.burst) {
			p.tokens = float64(p.burst)
		}
		if p.tokens >= float64(n) {
			p.tokens -= float64(n)
			p.mu.Unlock()
			return ctx.Err()
		}
		wait := time.Duration((float64(n) - p.tokens) * 8 / float64(p.bps) * float64(time.Second))
		p.mu.Unlock()
		if wait < time.Microsecond {
			wait = time.Microsecond
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}
