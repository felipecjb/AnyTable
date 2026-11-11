package input

import (
	"reflect"
	"testing"

	"anytable/common/protocol"
	"anytable/server/src/capture"
)

type call struct {
	kind string
	a, b int
	down bool
}

type fakeBackend struct{ calls []call }

func (f *fakeBackend) MoveTo(_ capture.Region, x, y int) error {
	f.calls = append(f.calls, call{kind: "move", a: x, b: y})
	return nil
}
func (f *fakeBackend) Button(b protocol.MouseButton, down bool) error {
	f.calls = append(f.calls, call{kind: "button", a: int(b), down: down})
	return nil
}
func (f *fakeBackend) Wheel(n int) error {
	f.calls = append(f.calls, call{kind: "wheel", a: n})
	return nil
}
func (f *fakeBackend) Key(code uint16, down bool) error {
	f.calls = append(f.calls, call{kind: "key", a: int(code), down: down})
	return nil
}
func (f *fakeBackend) Close() error { return nil }
func (f *fakeBackend) Name() string { return "fake" }

type fixedRegion capture.Region

func (r fixedRegion) Region() capture.Region { return capture.Region(r) }

func TestPixelMapsNormalizedToAbsolute(t *testing.T) {
	r := capture.Region{X: 1920, Y: 0, W: 1920, H: 1080, RootW: 3840, RootH: 1080}
	cases := []struct {
		nx, ny float32
		x, y   int
	}{
		{0, 0, 1920, 0},
		{1, 1, 3839, 1079},
		{0.5, 0.5, 1920 + 960, 540},
		{-3, 9, 1920, 1079}, // out of range is clamped
	}
	for _, c := range cases {
		if x, y := Pixel(r, c.nx, c.ny); x != c.x || y != c.y {
			t.Errorf("Pixel(%v,%v) = %d,%d want %d,%d", c.nx, c.ny, x, y, c.x, c.y)
		}
	}
}

func TestMouseEventSequenceAndReleaseAll(t *testing.T) {
	fb := &fakeBackend{}
	inj := NewWithBackend(fb, fixedRegion{W: 101, H: 101, RootW: 101, RootH: 101})

	if err := inj.Mouse(protocol.MouseEvent{X: 0.5, Y: 1, Button: protocol.MouseButtonLeft, State: protocol.ButtonPress, ScrollDelta: -2}); err != nil {
		t.Fatal(err)
	}
	want := []call{{kind: "move", a: 50, b: 100}, {kind: "button", a: int(protocol.MouseButtonLeft), down: true}, {kind: "wheel", a: -2}}
	if !reflect.DeepEqual(fb.calls, want) {
		t.Fatalf("got %+v want %+v", fb.calls, want)
	}

	fb.calls = nil
	if err := inj.Key(protocol.KeyboardEvent{Scancode: 30, State: protocol.KeyDown}); err != nil {
		t.Fatal(err)
	}
	if err := inj.Key(protocol.KeyboardEvent{Keycode: 48, State: protocol.KeyDown}); err != nil { // Scancode 0 -> Keycode
		t.Fatal(err)
	}
	if err := inj.Key(protocol.KeyboardEvent{Scancode: 48, State: protocol.KeyUp}); err != nil {
		t.Fatal(err)
	}
	fb.calls = nil
	inj.ReleaseAll() // key 30 and left button are still held
	if len(fb.calls) != 2 {
		t.Fatalf("ReleaseAll sent %+v, want key30 up + left up", fb.calls)
	}
	for _, c := range fb.calls {
		if c.down {
			t.Fatalf("ReleaseAll sent a press: %+v", c)
		}
	}
	fb.calls = nil
	inj.ReleaseAll()
	if len(fb.calls) != 0 {
		t.Fatal("second ReleaseAll must be a no-op")
	}
}

func TestInjectorErrors(t *testing.T) {
	inj := NewWithBackend(&fakeBackend{}, fixedRegion{})
	if err := inj.Mouse(protocol.MouseEvent{}); err != ErrNoDisplay {
		t.Fatalf("want ErrNoDisplay, got %v", err)
	}
	if err := inj.Key(protocol.KeyboardEvent{Scancode: 300, State: protocol.KeyDown}); err == nil {
		t.Fatal("want ErrBadKey for out-of-range code")
	}
}
