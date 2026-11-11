package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

type MouseButton uint8

const (
	MouseButtonNone   MouseButton = 0
	MouseButtonLeft   MouseButton = 1
	MouseButtonRight  MouseButton = 2
	MouseButtonMiddle MouseButton = 3

	ButtonNone   MouseButton = MouseButtonNone
	ButtonLeft   MouseButton = MouseButtonLeft
	ButtonRight  MouseButton = MouseButtonRight
	ButtonMiddle MouseButton = MouseButtonMiddle
)

type ButtonState uint8

const (
	ButtonRelease ButtonState = 0
	ButtonPress   ButtonState = 1
)

type KeyModifier uint8

const (
	ModNone    KeyModifier = 0
	ModCtrl    KeyModifier = 1 << 0
	ModAlt     KeyModifier = 1 << 1
	ModShift   KeyModifier = 1 << 2
	ModMeta    KeyModifier = 1 << 3
	ModAllMask KeyModifier = ModCtrl | ModAlt | ModShift | ModMeta
)

type KeyState uint8

const (
	KeyUp   KeyState = 0
	KeyDown KeyState = 1
)

const (
	// MousePacketSize: Magic(2) + Type(1) + X(4) + Y(4) + Button(1) + State(1) + ScrollDelta(2) = 15 bytes
	MousePacketSize = 15
	// KeyboardPacketSize: Magic(2) + Type(1) + Keycode(4) + Scancode(4) + Modifiers(1) + State(1) = 13 bytes
	KeyboardPacketSize = 13
)

var (
	ErrInvalidMouseCoords  = errors.New("protocol: mouse coordinates must be normalized in [0.0, 1.0]")
	ErrInvalidMouseButton  = errors.New("protocol: invalid mouse button")
	ErrInvalidButtonState  = errors.New("protocol: invalid mouse button state")
	ErrInvalidKeyModifiers = errors.New("protocol: invalid keyboard modifier bitmask")
	ErrInvalidKeyState     = errors.New("protocol: invalid keyboard state")
)

type MouseEvent struct {
	X           float32
	Y           float32
	Button      MouseButton
	State       ButtonState
	ScrollDelta int16
}

type KeyboardEvent struct {
	Keycode   uint32
	Scancode  uint32
	Modifiers KeyModifier
	State     KeyState
}

type InputPacket struct {
	Magic    [2]byte
	Type     PacketType
	Mouse    MouseEvent
	Keyboard KeyboardEvent
}

func NewMouseInputPacket(x, y float32, button MouseButton, state ButtonState, scrollDelta int16) *InputPacket {
	return &InputPacket{
		Magic: [2]byte{MagicByte0, MagicByte1},
		Type:  PacketTypeInputMouse,
		Mouse: MouseEvent{
			X:           x,
			Y:           y,
			Button:      button,
			State:       state,
			ScrollDelta: scrollDelta,
		},
	}
}

func NewKeyboardInputPacket(keycode, scancode uint32, modifiers KeyModifier, state KeyState) *InputPacket {
	return &InputPacket{
		Magic: [2]byte{MagicByte0, MagicByte1},
		Type:  PacketTypeInputKeyboard,
		Keyboard: KeyboardEvent{
			Keycode:   keycode,
			Scancode:  scancode,
			Modifiers: modifiers,
			State:     state,
		},
	}
}

func isValidNormalizedCoord(v float32) bool {
	f := float64(v)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return v >= 0.0 && v <= 1.0
}

func (p *InputPacket) Validate() error {
	if p.Magic != [2]byte{MagicByte0, MagicByte1} {
		return fmt.Errorf("%w: got %#x %#x", ErrInvalidMagic, p.Magic[0], p.Magic[1])
	}
	switch p.Type {
	case PacketTypeInputMouse:
		if !isValidNormalizedCoord(p.Mouse.X) || !isValidNormalizedCoord(p.Mouse.Y) {
			return fmt.Errorf("%w: X=%v Y=%v", ErrInvalidMouseCoords, p.Mouse.X, p.Mouse.Y)
		}
		if p.Mouse.Button > MouseButtonMiddle {
			return fmt.Errorf("%w: %d", ErrInvalidMouseButton, p.Mouse.Button)
		}
		if p.Mouse.State > ButtonPress {
			return fmt.Errorf("%w: %d", ErrInvalidButtonState, p.Mouse.State)
		}
		return nil
	case PacketTypeInputKeyboard:
		if p.Keyboard.Modifiers & ^ModAllMask != 0 {
			return fmt.Errorf("%w: %#x", ErrInvalidKeyModifiers, p.Keyboard.Modifiers)
		}
		if p.Keyboard.State > KeyDown {
			return fmt.Errorf("%w: %d", ErrInvalidKeyState, p.Keyboard.State)
		}
		return nil
	default:
		return fmt.Errorf("%w: expected INPUT_MOUSE(%#x) or INPUT_KEYBOARD(%#x), got %#x",
			ErrUnexpectedPacketType, PacketTypeInputMouse, PacketTypeInputKeyboard, p.Type)
	}
}

func (p *InputPacket) Serialize() ([]byte, error) {
	if p.Magic == [2]byte{0, 0} {
		p.Magic = [2]byte{MagicByte0, MagicByte1}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}

	switch p.Type {
	case PacketTypeInputMouse:
		var buf [MousePacketSize]byte
		buf[0] = p.Magic[0]
		buf[1] = p.Magic[1]
		buf[2] = uint8(p.Type)
		binary.BigEndian.PutUint32(buf[3:7], math.Float32bits(p.Mouse.X))
		binary.BigEndian.PutUint32(buf[7:11], math.Float32bits(p.Mouse.Y))
		buf[11] = uint8(p.Mouse.Button)
		buf[12] = uint8(p.Mouse.State)
		binary.BigEndian.PutUint16(buf[13:15], uint16(p.Mouse.ScrollDelta))
		return buf[:], nil

	case PacketTypeInputKeyboard:
		var buf [KeyboardPacketSize]byte
		buf[0] = p.Magic[0]
		buf[1] = p.Magic[1]
		buf[2] = uint8(p.Type)
		binary.BigEndian.PutUint32(buf[3:7], p.Keyboard.Keycode)
		binary.BigEndian.PutUint32(buf[7:11], p.Keyboard.Scancode)
		buf[11] = uint8(p.Keyboard.Modifiers)
		buf[12] = uint8(p.Keyboard.State)
		return buf[:], nil

	default:
		return nil, ErrUnexpectedPacketType
	}
}

func (p *InputPacket) MarshalBinary() ([]byte, error) {
	return p.Serialize()
}

func (p *InputPacket) UnmarshalBinary(data []byte) error {
	parsed, err := DeserializeInput(data)
	if err != nil {
		return err
	}
	*p = *parsed
	return nil
}

func DeserializeInput(data []byte) (*InputPacket, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("%w: need at least 3 bytes, got %d", ErrPacketTooShort, len(data))
	}

	magic := [2]byte{data[0], data[1]}
	if magic != [2]byte{MagicByte0, MagicByte1} {
		return nil, fmt.Errorf("%w: got %#x %#x", ErrInvalidMagic, magic[0], magic[1])
	}

	pktType := PacketType(data[2])
	switch pktType {
	case PacketTypeInputMouse:
		if len(data) < MousePacketSize {
			return nil, fmt.Errorf("%w: mouse packet requires %d bytes, got %d", ErrPacketTooShort, MousePacketSize, len(data))
		}
		if len(data) > MousePacketSize {
			return nil, fmt.Errorf("%w: mouse packet expected %d bytes, got %d", ErrPayloadMismatch, MousePacketSize, len(data))
		}
		pkt := &InputPacket{
			Magic: magic,
			Type:  PacketTypeInputMouse,
			Mouse: MouseEvent{
				X:           math.Float32frombits(binary.BigEndian.Uint32(data[3:7])),
				Y:           math.Float32frombits(binary.BigEndian.Uint32(data[7:11])),
				Button:      MouseButton(data[11]),
				State:       ButtonState(data[12]),
				ScrollDelta: int16(binary.BigEndian.Uint16(data[13:15])),
			},
		}
		if err := pkt.Validate(); err != nil {
			return nil, err
		}
		return pkt, nil

	case PacketTypeInputKeyboard:
		if len(data) < KeyboardPacketSize {
			return nil, fmt.Errorf("%w: keyboard packet requires %d bytes, got %d", ErrPacketTooShort, KeyboardPacketSize, len(data))
		}
		if len(data) > KeyboardPacketSize {
			return nil, fmt.Errorf("%w: keyboard packet expected %d bytes, got %d", ErrPayloadMismatch, KeyboardPacketSize, len(data))
		}
		pkt := &InputPacket{
			Magic: magic,
			Type:  PacketTypeInputKeyboard,
			Keyboard: KeyboardEvent{
				Keycode:   binary.BigEndian.Uint32(data[3:7]),
				Scancode:  binary.BigEndian.Uint32(data[7:11]),
				Modifiers: KeyModifier(data[11]),
				State:     KeyState(data[12]),
			},
		}
		if err := pkt.Validate(); err != nil {
			return nil, err
		}
		return pkt, nil

	default:
		return nil, fmt.Errorf("%w: expected INPUT_MOUSE(%#x) or INPUT_KEYBOARD(%#x), got %#x",
			ErrUnexpectedPacketType, PacketTypeInputMouse, PacketTypeInputKeyboard, pktType)
	}
}

func (p *InputPacket) Encode(w io.Writer) error {
	raw, err := p.Serialize()
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func DecodeInput(r io.Reader) (*InputPacket, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	pktType := PacketType(hdr[2])
	var totalSize int
	switch pktType {
	case PacketTypeInputMouse:
		totalSize = MousePacketSize
	case PacketTypeInputKeyboard:
		totalSize = KeyboardPacketSize
	default:
		return nil, fmt.Errorf("%w: got %#x", ErrUnexpectedPacketType, pktType)
	}

	buf := make([]byte, totalSize)
	copy(buf[:3], hdr[:])
	if _, err := io.ReadFull(r, buf[3:]); err != nil {
		return nil, err
	}
	return DeserializeInput(buf)
}
