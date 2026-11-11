package input

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"anytable/common/protocol"
)

// Sender enqueues serialized input packets without blocking the render thread.
type Sender func(raw []byte, urgent bool)

type buttonMapping struct {
	eb ebiten.MouseButton
	pr protocol.MouseButton
}

var trackedButtons = []buttonMapping{
	{ebiten.MouseButtonLeft, protocol.MouseButtonLeft},
	{ebiten.MouseButtonRight, protocol.MouseButtonRight},
	{ebiten.MouseButtonMiddle, protocol.MouseButtonMiddle},
}

// Capturer reads focused mouse and keyboard transitions and emits protocol packets.
type Capturer struct {
	hasPos      bool
	lastX       float32
	lastY       float32
	heldButtons map[protocol.MouseButton]bool
	heldKeys    map[ebiten.Key]uint32
	keysBuf     []ebiten.Key
}

// NewCapturer initializes an input capturer.
func NewCapturer() *Capturer {
	return &Capturer{
		heldButtons: make(map[protocol.MouseButton]bool),
		heldKeys:    make(map[ebiten.Key]uint32),
	}
}

// Update polls pointer/keyboard state once per frame and emits input packets via send.
func (c *Capturer) Update(rect CanvasRect, hudConsumed, focused bool, send Sender) {
	if !focused {
		c.ReleaseAll(send)
		return
	}

	c.captureMouse(rect, hudConsumed, send)
	c.captureKeyboard(send)
}

func (c *Capturer) captureMouse(rect CanvasRect, hudConsumed bool, send Sender) {
	if hudConsumed {
		return
	}
	imx, imy := ebiten.CursorPosition()
	nx, ny, inside, valid := NormalizeMouse(float64(imx), float64(imy), rect)
	if !valid {
		return
	}

	_, wheelY := ebiten.Wheel()
	scrollDelta := int16(math.Round(wheelY))

	moved := !c.hasPos || nx != c.lastX || ny != c.lastY
	if moved && (inside || len(c.heldButtons) > 0) {
		c.lastX, c.lastY, c.hasPos = nx, ny, true
		c.emitMouse(nx, ny, protocol.MouseButtonNone, protocol.ButtonRelease, 0, false, send)
	}

	for _, bm := range trackedButtons {
		if inpututil.IsMouseButtonJustPressed(bm.eb) && inside {
			c.heldButtons[bm.pr] = true
			c.lastX, c.lastY, c.hasPos = nx, ny, true
			c.emitMouse(nx, ny, bm.pr, protocol.ButtonPress, 0, true, send)
		}
		if inpututil.IsMouseButtonJustReleased(bm.eb) && c.heldButtons[bm.pr] {
			delete(c.heldButtons, bm.pr)
			c.emitMouse(nx, ny, bm.pr, protocol.ButtonRelease, 0, true, send)
		}
	}

	if scrollDelta != 0 && inside {
		c.emitMouse(nx, ny, protocol.MouseButtonNone, protocol.ButtonRelease, scrollDelta, true, send)
	}
}

func (c *Capturer) captureKeyboard(send Sender) {
	mods := CurrentModifiers()

	c.keysBuf = inpututil.AppendJustPressedKeys(c.keysBuf[:0])
	for _, k := range c.keysBuf {
		code, ok := EbitenKeyToEvdev(k)
		if !ok {
			continue
		}
		c.heldKeys[k] = code
		c.emitKey(uint32(k), code, mods, protocol.KeyDown, send)
	}

	c.keysBuf = inpututil.AppendJustReleasedKeys(c.keysBuf[:0])
	for _, k := range c.keysBuf {
		code, ok := EbitenKeyToEvdev(k)
		if !ok {
			continue
		}
		delete(c.heldKeys, k)
		c.emitKey(uint32(k), code, mods, protocol.KeyUp, send)
	}
}

// ReleaseAll emits KeyUp / ButtonRelease for any held inputs (on focus loss or disconnect).
func (c *Capturer) ReleaseAll(send Sender) {
	for k, code := range c.heldKeys {
		c.emitKey(uint32(k), code, protocol.ModNone, protocol.KeyUp, send)
		delete(c.heldKeys, k)
	}
	for btn := range c.heldButtons {
		c.emitMouse(c.lastX, c.lastY, btn, protocol.ButtonRelease, 0, true, send)
		delete(c.heldButtons, btn)
	}
	c.hasPos = false
}

func (c *Capturer) emitMouse(x, y float32, btn protocol.MouseButton, state protocol.ButtonState, scroll int16, urgent bool, send Sender) {
	if raw, err := protocol.NewMouseInputPacket(x, y, btn, state, scroll).Serialize(); err == nil {
		send(raw, urgent)
	}
}

func (c *Capturer) emitKey(keycode, scancode uint32, mods protocol.KeyModifier, state protocol.KeyState, send Sender) {
	if raw, err := protocol.NewKeyboardInputPacket(keycode, scancode, mods, state).Serialize(); err == nil {
		send(raw, true)
	}
}
