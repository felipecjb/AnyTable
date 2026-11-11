package stream

import (
	"context"
	"fmt"
	"log"
	"time"

	"anytable/server/src/capture"
)

const (
	maxConsecutiveErrors = 50
	errorBackoff         = 200 * time.Millisecond
)

// Pump grabs frames at the target FPS and publishes them to the hub.
// It sleeps (0% CPU) while nobody is subscribed.
type Pump struct {
	Hub *Hub
	FPS func() int
	Log *log.Logger
}

// Run captures until ctx ends (returns nil) or the source keeps failing (returns error).
func (p *Pump) Run(ctx context.Context, src capture.Source) error {
	next := time.Now()
	failures := 0
	for {
		if p.Hub.Count() == 0 {
			if err := p.Hub.WaitSubscribers(ctx); err != nil {
				return nil
			}
			next = time.Now()
		}
		if ctx.Err() != nil {
			return nil
		}
		f, err := src.Grab()
		if err != nil {
			failures++
			if failures >= maxConsecutiveErrors {
				return fmt.Errorf("stream: capture failing: %w", err)
			}
			if failures == 1 && p.Log != nil {
				p.Log.Printf("capture error (retrying): %v", err)
			}
			if !sleepCtx(ctx, errorBackoff) {
				return nil
			}
			continue
		}
		failures = 0
		p.Hub.Publish(f)
		f.Release()

		fps := p.FPS()
		if fps < 1 {
			fps = 1
		}
		next = next.Add(time.Second / time.Duration(fps))
		if d := time.Until(next); d > 0 {
			if !sleepCtx(ctx, d) {
				return nil
			}
		} else {
			next = time.Now() // behind schedule: do not burst to catch up
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
