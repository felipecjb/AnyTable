package stream

import (
	"bytes"
	"context"
	"image/jpeg"
	"sync"
	"testing"
	"time"

	"anytable/server/src/capture"
)

func solidFrame(w, h int, bgr bool, r, g, b byte) *capture.Frame {
	pix := make([]byte, w*h*4)
	for i := 0; i < len(pix); i += 4 {
		if bgr {
			pix[i], pix[i+1], pix[i+2] = b, g, r
		} else {
			pix[i], pix[i+1], pix[i+2] = r, g, b
		}
	}
	return capture.NewFrame(pix, w, h, bgr, nil)
}

func TestSlotKeepsOnlyFreshestFrame(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	for i := 1; i <= 5; i++ {
		f := solidFrame(2, 2, true, 0, 0, 0)
		f.Seq = uint64(i)
		h.Publish(f)
		f.Release()
	}
	f, ok := s.Take()
	if !ok || f.Seq != 5 {
		t.Fatalf("want freshest seq 5, got %v ok=%v", f, ok)
	}
	f.Release()
	if s.Dropped() != 4 {
		t.Fatalf("dropped = %d, want 4", s.Dropped())
	}
	h.Unsubscribe(s)
	if _, ok := s.Take(); ok {
		t.Fatal("Take on closed slot must return false")
	}
}

func TestFrameBufferRecycledAfterAllReleases(t *testing.T) {
	freed := 0
	f := capture.NewFrame(make([]byte, 16), 2, 2, true, func([]byte) { freed++ })
	h := NewHub()
	a, b := h.Subscribe(), h.Subscribe()
	h.Publish(f)
	f.Release()
	if freed != 0 {
		t.Fatal("freed while slots still hold the frame")
	}
	h.Unsubscribe(a)
	h.Unsubscribe(b)
	if freed != 1 {
		t.Fatalf("freed = %d, want exactly 1", freed)
	}
}

func TestEncoderRoundTripColorsAndScale(t *testing.T) {
	enc := NewEncoder(90)
	for _, bgr := range []bool{true, false} {
		data, err := enc.Encode(solidFrame(64, 48, bgr, 250, 10, 10), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 48 {
			t.Fatalf("bounds %v", img.Bounds())
		}
		r, g, b, _ := img.At(32, 24).RGBA()
		if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
			t.Fatalf("bgr=%v: expected red pixel, got %d %d %d", bgr, r>>8, g>>8, b>>8)
		}
	}
	data, err := enc.Encode(solidFrame(200, 100, true, 0, 0, 255), 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := jpeg.Decode(bytes.NewReader(data))
	if img.Bounds().Dx() != 100 || img.Bounds().Dy() != 50 {
		t.Fatalf("scaled bounds %v, want 100x50", img.Bounds())
	}
}

func TestFitSize(t *testing.T) {
	cases := []struct{ w, h, mw, mh, ew, eh int }{
		{1920, 1080, 0, 0, 1920, 1080},
		{1920, 1080, 3840, 2160, 1920, 1080},
		{1920, 1080, 960, 960, 960, 540},
		{1920, 1080, 1920, 540, 960, 540},
	}
	for _, c := range cases {
		if w, h := FitSize(c.w, c.h, c.mw, c.mh); w != c.ew || h != c.eh {
			t.Errorf("FitSize(%+v) = %dx%d", c, w, h)
		}
	}
}

func TestAdaptiveQuality(t *testing.T) {
	e := NewEncoder(80)
	budget := 16 * time.Millisecond
	for i := 0; i < 20; i++ {
		e.Adapt(50*time.Millisecond, budget)
	}
	if e.Quality() != 40 {
		t.Fatalf("quality floor = %d, want 40 (base-40)", e.Quality())
	}
	for i := 0; i < 100; i++ {
		e.Adapt(time.Millisecond, budget)
	}
	if e.Quality() != 80 {
		t.Fatalf("quality should recover to base, got %d", e.Quality())
	}
}

type fakeSource struct {
	mu sync.Mutex
	n  int
}

func (f *fakeSource) Grab() (*capture.Frame, error) {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	return solidFrame(4, 4, true, 1, 2, 3), nil
}
func (f *fakeSource) Region() capture.Region { return capture.Region{W: 4, H: 4, RootW: 4, RootH: 4} }
func (f *fakeSource) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func TestPumpIdlesWithoutSubscribersAndPacesToFPS(t *testing.T) {
	h := NewHub()
	src := &fakeSource{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (&Pump{Hub: h, FPS: func() int { return 50 }}).Run(ctx, src) }()

	time.Sleep(100 * time.Millisecond)
	if src.count() != 0 {
		t.Fatalf("pump captured %d frames with no subscribers", src.count())
	}
	s := h.Subscribe()
	time.Sleep(400 * time.Millisecond)
	if n := src.count(); n < 10 || n > 30 {
		t.Fatalf("captured %d frames in 400ms at 50fps (expected ~20)", n)
	}
	h.Unsubscribe(s)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
