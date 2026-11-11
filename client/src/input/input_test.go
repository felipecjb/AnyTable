package input

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"anytable/common/protocol"
)

func TestNormalizeMouse(t *testing.T) {
	rect := CanvasRect{X: 100, Y: 50, W: 800, H: 400}

	x, y, in, ok := NormalizeMouse(500, 250, rect)
	if !ok || !in || x != 0.5 || y != 0.5 {
		t.Fatalf("center got (%v,%v) inside=%v ok=%v", x, y, in, ok)
	}

	x, y, in, ok = NormalizeMouse(50, 500, rect)
	if !ok || in || x != 0.0 || y != 1.0 {
		t.Fatalf("clamped edge got (%v,%v) inside=%v ok=%v", x, y, in, ok)
	}

	_, _, _, ok = NormalizeMouse(10, 10, CanvasRect{})
	if ok {
		t.Fatal("zero-size canvas rect must be invalid")
	}
}

func TestKeyMappingAndCtrlAltDel(t *testing.T) {
	if code, ok := EbitenKeyToEvdev(ebiten.KeyA); !ok || code != 30 {
		t.Fatalf("KeyA evdev = %d ok=%v, want 30", code, ok)
	}
	if code, ok := EbitenKeyToEvdev(ebiten.KeyControlLeft); !ok || code != 29 {
		t.Fatalf("KeyControlLeft evdev = %d ok=%v, want 29", code, ok)
	}
	if _, ok := EbitenKeyToEvdev(ebiten.KeyShift); ok {
		t.Fatal("virtual KeyShift must not map directly to avoid duplicate events")
	}

	pkts := CtrlAltDelPackets()
	if len(pkts) != 6 {
		t.Fatalf("CtrlAltDel packet count = %d, want 6", len(pkts))
	}
	for i, raw := range pkts {
		p, err := protocol.DeserializeInput(raw)
		if err != nil {
			t.Fatalf("packet %d invalid: %v", i, err)
		}
		if p.Type != protocol.PacketTypeInputKeyboard {
			t.Fatalf("packet %d type = %v", i, p.Type)
		}
	}
}

func TestReleaseAllClearsHeldState(t *testing.T) {
	c := NewCapturer()
	c.heldKeys[ebiten.KeyA] = 30
	c.heldButtons[protocol.MouseButtonLeft] = true
	c.lastX, c.lastY = 0.25, 0.75

	var emitted [][]byte
	c.ReleaseAll(func(raw []byte, urgent bool) {
		if !urgent {
			t.Fatal("release packets must be urgent")
		}
		emitted = append(emitted, raw)
	})
	if len(emitted) != 2 {
		t.Fatalf("emitted %d release packets, want 2", len(emitted))
	}
	if len(c.heldKeys) != 0 || len(c.heldButtons) != 0 {
		t.Fatal("held maps not cleared after ReleaseAll")
	}
}
