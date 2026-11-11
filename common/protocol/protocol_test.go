package protocol

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"
)

func TestFramePacketRoundTrip(t *testing.T) {
	payload := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0xFF, 0xD9}

	for _, encoding := range []uint8{EncodingJPEG, EncodingLZ4} {
		orig := &FramePacket{
			Magic:         [2]byte{MagicByte0, MagicByte1},
			Type:          FRAME,
			SequenceID:    42001,
			Timestamp:     1700000000123456,
			Encoding:      encoding,
			PayloadLength: uint32(len(payload)),
			Payload:       payload,
		}

		raw, err := orig.Serialize()
		if err != nil {
			t.Fatalf("Serialize failed for encoding %d: %v", encoding, err)
		}
		if len(raw) != FrameHeaderSize+len(payload) {
			t.Fatalf("unexpected serialized size: got %d, want %d", len(raw), FrameHeaderSize+len(payload))
		}
		if raw[0] != 0x41 || raw[1] != 0x54 {
			t.Fatalf("invalid magic header bytes: %#x %#x", raw[0], raw[1])
		}

		decoded, err := DeserializeFrame(raw)
		if err != nil {
			t.Fatalf("DeserializeFrame failed: %v", err)
		}
		if decoded.Magic != orig.Magic ||
			decoded.Type != orig.Type ||
			decoded.SequenceID != orig.SequenceID ||
			decoded.Timestamp != orig.Timestamp ||
			decoded.Encoding != orig.Encoding ||
			decoded.PayloadLength != orig.PayloadLength ||
			!bytes.Equal(decoded.Payload, orig.Payload) {
			t.Fatalf("round-trip mismatch:\ngot:  %+v\nwant: %+v", decoded, orig)
		}

		var stream bytes.Buffer
		if err := orig.Encode(&stream); err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		streamDecoded, err := DecodeFrame(&stream)
		if err != nil {
			t.Fatalf("DecodeFrame failed: %v", err)
		}
		if !bytes.Equal(streamDecoded.Payload, orig.Payload) || streamDecoded.SequenceID != orig.SequenceID {
			t.Fatalf("stream round-trip mismatch")
		}
	}
}

func TestFramePacketValidationErrors(t *testing.T) {
	pkt := NewFramePacket(1, 99, []byte{1, 2, 3})
	if _, err := pkt.Serialize(); !errors.Is(err, ErrInvalidEncoding) {
		t.Fatalf("expected ErrInvalidEncoding, got %v", err)
	}

	if _, err := DeserializeFrame([]byte{0x41, 0x54}); !errors.Is(err, ErrPacketTooShort) {
		t.Fatalf("expected ErrPacketTooShort, got %v", err)
	}

	valid := NewFramePacket(1, EncodingJPEG, []byte{1, 2, 3, 4})
	raw, err := valid.Serialize()
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}
	raw[0] = 0x00
	if _, err := DeserializeFrame(raw); !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("expected ErrInvalidMagic, got %v", err)
	}
}

func TestPingPongPacketRoundTripAndRTT(t *testing.T) {
	for _, pktType := range []PacketType{PING, PONG} {
		orig := &PingPacket{
			Magic:      [2]byte{MagicByte0, MagicByte1},
			Type:       pktType,
			SequenceID: 999,
			Timestamp:  NowMicroseconds() - 8500,
		}

		raw, err := orig.Serialize()
		if err != nil {
			t.Fatalf("Serialize failed for %v: %v", pktType, err)
		}
		if len(raw) >= 32 {
			t.Fatalf("ping payload must be < 32 bytes, got %d", len(raw))
		}
		if len(raw) != PingPacketSize {
			t.Fatalf("unexpected ping packet size: got %d, want %d", len(raw), PingPacketSize)
		}

		decoded, err := DeserializePing(raw)
		if err != nil {
			t.Fatalf("DeserializePing failed: %v", err)
		}
		if *decoded != *orig {
			t.Fatalf("ping round-trip mismatch: got %+v, want %+v", decoded, orig)
		}

		var buf bytes.Buffer
		if err := orig.Encode(&buf); err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		fromStream, err := DecodePing(&buf)
		if err != nil {
			t.Fatalf("DecodePing failed: %v", err)
		}
		if *fromStream != *orig {
			t.Fatalf("stream ping mismatch: got %+v, want %+v", fromStream, orig)
		}
	}

	sent := NowMicroseconds() - 12000 // 12ms ago
	rtt := CalculateRTT(sent)
	if rtt < 10*time.Millisecond || rtt > 5*time.Second {
		t.Fatalf("unexpected RTT calculation: %v", rtt)
	}

	future := NowMicroseconds() + 1000000
	if rttFuture := CalculateRTT(future); rttFuture != 0 {
		t.Fatalf("expected 0 RTT for future timestamp, got %v", rttFuture)
	}
}

func TestInputMousePacketRoundTrip(t *testing.T) {
	buttons := []MouseButton{MouseButtonNone, MouseButtonLeft, MouseButtonRight, MouseButtonMiddle}
	states := []ButtonState{ButtonPress, ButtonRelease}

	for _, btn := range buttons {
		for _, st := range states {
			orig := NewMouseInputPacket(0.5, 0.75, btn, st, -120)
			raw, err := orig.Serialize()
			if err != nil {
				t.Fatalf("Serialize mouse failed: %v", err)
			}
			if len(raw) != MousePacketSize {
				t.Fatalf("unexpected mouse packet size: got %d, want %d", len(raw), MousePacketSize)
			}

			decoded, err := DeserializeInput(raw)
			if err != nil {
				t.Fatalf("DeserializeInput mouse failed: %v", err)
			}
			if decoded.Type != INPUT_MOUSE ||
				decoded.Mouse.X != orig.Mouse.X ||
				decoded.Mouse.Y != orig.Mouse.Y ||
				decoded.Mouse.Button != orig.Mouse.Button ||
				decoded.Mouse.State != orig.Mouse.State ||
				decoded.Mouse.ScrollDelta != orig.Mouse.ScrollDelta {
				t.Fatalf("mouse round-trip mismatch: got %+v, want %+v", decoded, orig)
			}

			var stream bytes.Buffer
			if err := orig.Encode(&stream); err != nil {
				t.Fatalf("Encode mouse failed: %v", err)
			}
			streamDecoded, err := DecodeInput(&stream)
			if err != nil {
				t.Fatalf("DecodeInput mouse failed: %v", err)
			}
			if streamDecoded.Mouse != orig.Mouse {
				t.Fatalf("stream mouse mismatch: got %+v, want %+v", streamDecoded.Mouse, orig.Mouse)
			}
		}
	}

	invalidCoord := NewMouseInputPacket(1.5, 0.2, MouseButtonLeft, ButtonPress, 0)
	if _, err := invalidCoord.Serialize(); !errors.Is(err, ErrInvalidMouseCoords) {
		t.Fatalf("expected ErrInvalidMouseCoords for X=1.5, got %v", err)
	}

	nanCoord := NewMouseInputPacket(float32(math.NaN()), 0.5, MouseButtonLeft, ButtonPress, 0)
	if _, err := nanCoord.Serialize(); !errors.Is(err, ErrInvalidMouseCoords) {
		t.Fatalf("expected ErrInvalidMouseCoords for NaN, got %v", err)
	}
}

func TestInputKeyboardPacketRoundTrip(t *testing.T) {
	mods := ModCtrl | ModAlt | ModShift | ModMeta
	for _, st := range []KeyState{KeyDown, KeyUp} {
		orig := NewKeyboardInputPacket(65, 38, mods, st)
		raw, err := orig.Serialize()
		if err != nil {
			t.Fatalf("Serialize keyboard failed: %v", err)
		}
		if len(raw) != KeyboardPacketSize {
			t.Fatalf("unexpected keyboard packet size: got %d, want %d", len(raw), KeyboardPacketSize)
		}

		decoded, err := DeserializeInput(raw)
		if err != nil {
			t.Fatalf("DeserializeInput keyboard failed: %v", err)
		}
		if decoded.Type != INPUT_KEYBOARD || decoded.Keyboard != orig.Keyboard {
			t.Fatalf("keyboard round-trip mismatch: got %+v, want %+v", decoded, orig)
		}

		var stream bytes.Buffer
		if err := orig.Encode(&stream); err != nil {
			t.Fatalf("Encode keyboard failed: %v", err)
		}
		streamDecoded, err := DecodeInput(&stream)
		if err != nil {
			t.Fatalf("DecodeInput keyboard failed: %v", err)
		}
		if streamDecoded.Keyboard != orig.Keyboard {
			t.Fatalf("stream keyboard mismatch: got %+v, want %+v", streamDecoded.Keyboard, orig.Keyboard)
		}
	}
}

func TestHandshakeAuthPacketRoundTrip(t *testing.T) {
	pin := "482910"
	orig := NewHandshakePacket(pin, 1920, 1080)
	orig.Authenticated = true

	raw, err := orig.Serialize()
	if err != nil {
		t.Fatalf("Serialize auth failed: %v", err)
	}
	if len(raw) != AuthPacketSize {
		t.Fatalf("unexpected auth packet size: got %d, want %d", len(raw), AuthPacketSize)
	}

	decoded, err := DeserializeHandshake(raw)
	if err != nil {
		t.Fatalf("DeserializeHandshake failed: %v", err)
	}
	if *decoded != *orig {
		t.Fatalf("auth round-trip mismatch: got %+v, want %+v", decoded, orig)
	}

	if err := decoded.VerifyHandshake(pin); err != nil {
		t.Fatalf("VerifyHandshake with valid PIN failed: %v", err)
	}
	if err := decoded.VerifyHandshake("000000"); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("expected ErrAuthFailed for wrong PIN, got %v", err)
	}

	decoded.Version = 999
	if err := decoded.CheckVersion(); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("expected ErrVersionMismatch, got %v", err)
	}

	invalidRes := NewHandshakePacket(pin, 0, 1080)
	if _, err := invalidRes.Serialize(); !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("expected ErrInvalidResolution, got %v", err)
	}

	var stream bytes.Buffer
	if err := orig.Encode(&stream); err != nil {
		t.Fatalf("Encode auth failed: %v", err)
	}
	streamDecoded, err := DecodeHandshake(&stream)
	if err != nil {
		t.Fatalf("DecodeHandshake failed: %v", err)
	}
	if *streamDecoded != *orig {
		t.Fatalf("stream auth mismatch: got %+v, want %+v", streamDecoded, orig)
	}
}
