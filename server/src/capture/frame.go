// Package capture grabs the primary X11 display as raw 32-bit pixel frames.
package capture

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// Region is the captured rectangle (primary monitor) inside the X root window.
// RootW/RootH are the full root window size; absolute input devices map to it.
type Region struct {
	X, Y, W, H   int
	RootW, RootH int
}

// Frame is one raw captured image, 4 bytes per pixel, stride = W*4.
// BGR=true: byte order B,G,R,X. BGR=false: R,G,B,X.
// Frames are reference counted: whoever Retains must Release.
type Frame struct {
	Pix    []byte
	W, H   int
	BGR    bool
	Seq    uint64
	TimeUs uint64 // capture time, microseconds since Unix epoch

	refs int32
	free func([]byte)
}

// NewFrame returns a frame holding one reference. free (optional) receives Pix when the last reference drops.
func NewFrame(pix []byte, w, h int, bgr bool, free func([]byte)) *Frame {
	return &Frame{Pix: pix, W: w, H: h, BGR: bgr, TimeUs: nowMicros(), refs: 1, free: free}
}

// Retain adds a reference.
func (f *Frame) Retain() { atomic.AddInt32(&f.refs, 1) }

// Release drops a reference; the last one recycles the pixel buffer.
func (f *Frame) Release() {
	if atomic.AddInt32(&f.refs, -1) == 0 && f.free != nil {
		f.free(f.Pix)
		f.Pix = nil
	}
}

// Source produces frames of the current capture region.
type Source interface {
	Grab() (*Frame, error)
	Region() Region
}

func nowMicros() uint64 { return uint64(time.Now().UnixNano() / 1000) }

// ErrTimeout is returned when the X server does not answer in time (dead or frozen connection).
var ErrTimeout = errors.New("capture: X11 request timed out")

// call runs fn with a timeout and panic guard. xgb replies block forever once the
// connection dies, so every X round trip must go through here.
func call[T any](d time.Duration, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("capture: X11 call panicked: %v", r)}
			}
		}()
		v, err := fn()
		ch <- result{v, err}
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-t.C:
		var zero T
		return zero, ErrTimeout
	}
}
