package input

import (
	"github.com/hajimehoshi/ebiten/v2"

	"anytable/common/protocol"
)

// Linux evdev KEY_* scancodes expected by server/src/input (uinput & XTest keycode-8).
const (
	evdevEsc       uint32 = 1
	evdevLeftCtrl  uint32 = 29
	evdevLeftShift uint32 = 42
	evdevLeftAlt   uint32 = 56
	evdevDelete    uint32 = 111
)

var ebitenToEvdev = map[ebiten.Key]uint32{
	ebiten.KeyEscape:         evdevEsc,
	ebiten.KeyDigit1:         2,
	ebiten.KeyDigit2:         3,
	ebiten.KeyDigit3:         4,
	ebiten.KeyDigit4:         5,
	ebiten.KeyDigit5:         6,
	ebiten.KeyDigit6:         7,
	ebiten.KeyDigit7:         8,
	ebiten.KeyDigit8:         9,
	ebiten.KeyDigit9:         10,
	ebiten.KeyDigit0:         11,
	ebiten.KeyMinus:          12,
	ebiten.KeyEqual:          13,
	ebiten.KeyBackspace:      14,
	ebiten.KeyTab:            15,
	ebiten.KeyQ:              16,
	ebiten.KeyW:              17,
	ebiten.KeyE:              18,
	ebiten.KeyR:              19,
	ebiten.KeyT:              20,
	ebiten.KeyY:              21,
	ebiten.KeyU:              22,
	ebiten.KeyI:              23,
	ebiten.KeyO:              24,
	ebiten.KeyP:              25,
	ebiten.KeyBracketLeft:    26,
	ebiten.KeyBracketRight:   27,
	ebiten.KeyEnter:          28,
	ebiten.KeyControlLeft:    evdevLeftCtrl,
	ebiten.KeyA:              30,
	ebiten.KeyS:              31,
	ebiten.KeyD:              32,
	ebiten.KeyF:              33,
	ebiten.KeyG:              34,
	ebiten.KeyH:              35,
	ebiten.KeyJ:              36,
	ebiten.KeyK:              37,
	ebiten.KeyL:              38,
	ebiten.KeySemicolon:      39,
	ebiten.KeyQuote:          40,
	ebiten.KeyBackquote:      41,
	ebiten.KeyShiftLeft:      evdevLeftShift,
	ebiten.KeyBackslash:      43,
	ebiten.KeyZ:              44,
	ebiten.KeyX:              45,
	ebiten.KeyC:              46,
	ebiten.KeyV:              47,
	ebiten.KeyB:              48,
	ebiten.KeyN:              49,
	ebiten.KeyM:              50,
	ebiten.KeyComma:          51,
	ebiten.KeyPeriod:         52,
	ebiten.KeySlash:          53,
	ebiten.KeyShiftRight:     54,
	ebiten.KeyNumpadMultiply: 55,
	ebiten.KeyAltLeft:        evdevLeftAlt,
	ebiten.KeySpace:          57,
	ebiten.KeyCapsLock:       58,
	ebiten.KeyF1:             59,
	ebiten.KeyF2:             60,
	ebiten.KeyF3:             61,
	ebiten.KeyF4:             62,
	ebiten.KeyF5:             63,
	ebiten.KeyF6:             64,
	ebiten.KeyF7:             65,
	ebiten.KeyF8:             66,
	ebiten.KeyF9:             67,
	ebiten.KeyF10:            68,
	ebiten.KeyNumLock:        69,
	ebiten.KeyScrollLock:     70,
	ebiten.KeyNumpad7:        71,
	ebiten.KeyNumpad8:        72,
	ebiten.KeyNumpad9:        73,
	ebiten.KeyNumpadSubtract: 74,
	ebiten.KeyNumpad4:        75,
	ebiten.KeyNumpad5:        76,
	ebiten.KeyNumpad6:        77,
	ebiten.KeyNumpadAdd:      78,
	ebiten.KeyNumpad1:        79,
	ebiten.KeyNumpad2:        80,
	ebiten.KeyNumpad3:        81,
	ebiten.KeyNumpad0:        82,
	ebiten.KeyNumpadDecimal:  83,
	ebiten.KeyIntlBackslash:  86,
	ebiten.KeyF11:            87,
	ebiten.KeyF12:            88,
	ebiten.KeyNumpadEnter:    96,
	ebiten.KeyControlRight:   97,
	ebiten.KeyNumpadDivide:   98,
	ebiten.KeyPrintScreen:    99,
	ebiten.KeyAltRight:       100,
	ebiten.KeyHome:           102,
	ebiten.KeyArrowUp:        103,
	ebiten.KeyPageUp:         104,
	ebiten.KeyArrowLeft:      105,
	ebiten.KeyArrowRight:     106,
	ebiten.KeyEnd:            107,
	ebiten.KeyArrowDown:      108,
	ebiten.KeyPageDown:       109,
	ebiten.KeyInsert:         110,
	ebiten.KeyDelete:         evdevDelete,
	ebiten.KeyNumpadEqual:    117,
	ebiten.KeyPause:          119,
	ebiten.KeyMetaLeft:       125,
	ebiten.KeyMetaRight:      126,
	ebiten.KeyContextMenu:    127,
	ebiten.KeyF13:            183,
	ebiten.KeyF14:            184,
	ebiten.KeyF15:            185,
	ebiten.KeyF16:            186,
	ebiten.KeyF17:            187,
	ebiten.KeyF18:            188,
	ebiten.KeyF19:            189,
	ebiten.KeyF20:            190,
	ebiten.KeyF21:            191,
	ebiten.KeyF22:            192,
	ebiten.KeyF23:            193,
	ebiten.KeyF24:            194,
}

// EbitenKeyToEvdev converts an Ebitengine physical key into a Linux evdev KEY_* scancode.
func EbitenKeyToEvdev(k ebiten.Key) (uint32, bool) {
	code, ok := ebitenToEvdev[k]
	return code, ok
}

// CurrentModifiers inspects currently pressed modifier keys into a protocol bitmask.
func CurrentModifiers() protocol.KeyModifier {
	var m protocol.KeyModifier
	if ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight) {
		m |= protocol.ModCtrl
	}
	if ebiten.IsKeyPressed(ebiten.KeyAltLeft) || ebiten.IsKeyPressed(ebiten.KeyAltRight) {
		m |= protocol.ModAlt
	}
	if ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight) {
		m |= protocol.ModShift
	}
	if ebiten.IsKeyPressed(ebiten.KeyMetaLeft) || ebiten.IsKeyPressed(ebiten.KeyMetaRight) {
		m |= protocol.ModMeta
	}
	return m
}

// CtrlAltDelPackets returns the serialized key press/release sequence for Ctrl+Alt+Del.
func CtrlAltDelPackets() [][]byte {
	mods := protocol.ModCtrl | protocol.ModAlt
	seq := []*protocol.InputPacket{
		protocol.NewKeyboardInputPacket(uint32(ebiten.KeyControlLeft), evdevLeftCtrl, protocol.ModCtrl, protocol.KeyDown),
		protocol.NewKeyboardInputPacket(uint32(ebiten.KeyAltLeft), evdevLeftAlt, mods, protocol.KeyDown),
		protocol.NewKeyboardInputPacket(uint32(ebiten.KeyDelete), evdevDelete, mods, protocol.KeyDown),
		protocol.NewKeyboardInputPacket(uint32(ebiten.KeyDelete), evdevDelete, mods, protocol.KeyUp),
		protocol.NewKeyboardInputPacket(uint32(ebiten.KeyAltLeft), evdevLeftAlt, protocol.ModCtrl, protocol.KeyUp),
		protocol.NewKeyboardInputPacket(uint32(ebiten.KeyControlLeft), evdevLeftCtrl, protocol.ModNone, protocol.KeyUp),
	}
	out := make([][]byte, 0, len(seq))
	for _, p := range seq {
		if raw, err := p.Serialize(); err == nil {
			out = append(out, raw)
		}
	}
	return out
}
