// Package input injects remote mouse/keyboard events into the host: /dev/uinput first, X11 XTest as fallback.
//
// Key convention (client side): KeyboardEvent.Scancode carries the Linux evdev KEY_* code
// (1..255); Keycode is used only when Scancode is 0. Modifier keys are sent as their own
// key events, so KeyboardEvent.Modifiers is informational and not injected separately.
// MouseEvent.ScrollDelta is in wheel notches, positive = scroll up.
package input

import (
	"errors"
	"fmt"
	"sync"

	"github.com/jezek/xgb"

	"anytable/common/protocol"
	"anytable/server/src/capture"
)

// Backend is a low-level injection driver.
type Backend interface {
	MoveTo(r capture.Region, px, py int) error // px,py are absolute root-window pixels
	Button(b protocol.MouseButton, down bool) error
	Wheel(notches int) error
	Key(code uint16, down bool) error
	Close() error
	Name() string
}

// RegionSource tells the injector which screen rectangle normalized coordinates refer to.
type RegionSource interface{ Region() capture.Region }

var (
	// ErrNoDisplay is returned while the capture region is unknown.
	ErrNoDisplay = errors.New("input: no display geometry available")
	// ErrBadKey is returned for key codes outside 1..255.
	ErrBadKey = errors.New("input: key code out of range")
)

// Injector translates protocol events to a Backend and remembers held keys/buttons.
type Injector struct {
	b  Backend
	rs RegionSource

	mu   sync.Mutex
	keys map[uint16]struct{}
	btns map[protocol.MouseButton]struct{}
}

// New builds an injector: uinput if available, otherwise XTest on conn.
func New(conn *xgb.Conn, rs RegionSource, logf func(format string, args ...interface{})) (*Injector, error) {
	b, err := newUinput()
	if err != nil {
		if logf != nil {
			logf("uinput unavailable (%v); falling back to XTest", err)
		}
		b, err = newXTest(conn)
		if err != nil {
			return nil, fmt.Errorf("input: no injection backend available: %w", err)
		}
	}
	return NewWithBackend(b, rs), nil
}

// NewWithBackend wraps an arbitrary backend (used by tests and New).
func NewWithBackend(b Backend, rs RegionSource) *Injector {
	return &Injector{b: b, rs: rs, keys: map[uint16]struct{}{}, btns: map[protocol.MouseButton]struct{}{}}
}

// Name returns the backend name ("uinput" or "xtest").
func (i *Injector) Name() string { return i.b.Name() }

// Pixel converts normalized (0..1) coordinates into absolute root-window pixels inside region r.
func Pixel(r capture.Region, nx, ny float32) (int, int) {
	return r.X + scale(nx, r.W), r.Y + scale(ny, r.H)
}

func scale(n float32, size int) int {
	if size <= 1 {
		return 0
	}
	v := int(n*float32(size-1) + 0.5)
	if v < 0 {
		return 0
	}
	if v > size-1 {
		return size - 1
	}
	return v
}

// Mouse injects a pointer move plus optional button change and wheel scroll.
func (i *Injector) Mouse(ev protocol.MouseEvent) error {
	r := i.rs.Region()
	if r.W <= 0 || r.H <= 0 {
		return ErrNoDisplay
	}
	px, py := Pixel(r, ev.X, ev.Y)
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.b.MoveTo(r, px, py); err != nil {
		return err
	}
	if ev.Button != protocol.MouseButtonNone {
		down := ev.State == protocol.ButtonPress
		if err := i.b.Button(ev.Button, down); err != nil {
			return err
		}
		if down {
			i.btns[ev.Button] = struct{}{}
		} else {
			delete(i.btns, ev.Button)
		}
	}
	if ev.ScrollDelta != 0 {
		return i.b.Wheel(int(ev.ScrollDelta))
	}
	return nil
}

// Key injects a key press or release.
func (i *Injector) Key(ev protocol.KeyboardEvent) error {
	code := ev.Scancode
	if code == 0 {
		code = ev.Keycode
	}
	if code < 1 || code > 255 {
		return fmt.Errorf("%w: %d", ErrBadKey, code)
	}
	down := ev.State == protocol.KeyDown
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.b.Key(uint16(code), down); err != nil {
		return err
	}
	if down {
		i.keys[uint16(code)] = struct{}{}
	} else {
		delete(i.keys, uint16(code))
	}
	return nil
}

// ReleaseAll releases every key and button still held (call when a client disconnects).
func (i *Injector) ReleaseAll() {
	i.mu.Lock()
	defer i.mu.Unlock()
	for k := range i.keys {
		_ = i.b.Key(k, false)
	}
	for b := range i.btns {
		_ = i.b.Button(b, false)
	}
	i.keys = map[uint16]struct{}{}
	i.btns = map[protocol.MouseButton]struct{}{}
}

// Close releases held inputs and closes the backend.
func (i *Injector) Close() error {
	i.ReleaseAll()
	return i.b.Close()
}
