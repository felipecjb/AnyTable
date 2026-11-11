package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	MagicByte0 uint8 = 0x41 // 'A'
	MagicByte1 uint8 = 0x54 // 'T'
)

type PacketType uint8

const (
	PacketTypeFrame         PacketType = 0x01
	PacketTypePing          PacketType = 0x02
	PacketTypePong          PacketType = 0x03
	PacketTypeInputMouse    PacketType = 0x04
	PacketTypeInputKeyboard PacketType = 0x05
	PacketTypeAuth          PacketType = 0x06

	FRAME          PacketType = PacketTypeFrame
	PING           PacketType = PacketTypePing
	PONG           PacketType = PacketTypePong
	INPUT_MOUSE    PacketType = PacketTypeInputMouse
	INPUT_KEYBOARD PacketType = PacketTypeInputKeyboard
	AUTH           PacketType = PacketTypeAuth
)

const (
	EncodingJPEG uint8 = 0
	EncodingLZ4  uint8 = 1
)

const (
	// FrameHeaderSize: Magic(2) + Type(1) + SequenceID(8) + Timestamp(8) + Encoding(1) + PayloadLength(4) = 24 bytes
	FrameHeaderSize     = 24
	MaxFramePayloadSize = 64 * 1024 * 1024 // 64 MiB upper bound
)

var (
	ErrInvalidMagic         = errors.New("protocol: invalid magic bytes")
	ErrUnexpectedPacketType = errors.New("protocol: unexpected packet type")
	ErrPacketTooShort       = errors.New("protocol: packet too short")
	ErrInvalidEncoding      = errors.New("protocol: invalid frame encoding flag")
	ErrPayloadMismatch      = errors.New("protocol: payload length mismatch")
	ErrPayloadTooLarge      = errors.New("protocol: frame payload exceeds maximum allowed size")
)

type FramePacket struct {
	Magic         [2]byte
	Type          PacketType
	SequenceID    uint64
	Timestamp     uint64
	Encoding      uint8
	PayloadLength uint32
	Payload       []byte
}

func NowMicroseconds() uint64 {
	return uint64(time.Now().UnixNano() / 1000)
}

func NewFramePacket(seqID uint64, encoding uint8, payload []byte) *FramePacket {
	return &FramePacket{
		Magic:         [2]byte{MagicByte0, MagicByte1},
		Type:          PacketTypeFrame,
		SequenceID:    seqID,
		Timestamp:     NowMicroseconds(),
		Encoding:      encoding,
		PayloadLength: uint32(len(payload)),
		Payload:       payload,
	}
}

func (f *FramePacket) Validate() error {
	if f.Magic != [2]byte{MagicByte0, MagicByte1} {
		return fmt.Errorf("%w: got %#x %#x", ErrInvalidMagic, f.Magic[0], f.Magic[1])
	}
	if f.Type != PacketTypeFrame {
		return fmt.Errorf("%w: expected %#x, got %#x", ErrUnexpectedPacketType, PacketTypeFrame, f.Type)
	}
	if f.Encoding != EncodingJPEG && f.Encoding != EncodingLZ4 {
		return fmt.Errorf("%w: %d", ErrInvalidEncoding, f.Encoding)
	}
	if uint64(len(f.Payload)) > MaxFramePayloadSize {
		return ErrPayloadTooLarge
	}
	if f.PayloadLength != uint32(len(f.Payload)) {
		return fmt.Errorf("%w: header=%d actual=%d", ErrPayloadMismatch, f.PayloadLength, len(f.Payload))
	}
	return nil
}

func (f *FramePacket) Serialize() ([]byte, error) {
	if f.Magic == [2]byte{0, 0} {
		f.Magic = [2]byte{MagicByte0, MagicByte1}
	}
	if f.Type == 0 {
		f.Type = PacketTypeFrame
	}
	f.PayloadLength = uint32(len(f.Payload))

	if err := f.Validate(); err != nil {
		return nil, err
	}

	buf := make([]byte, FrameHeaderSize+len(f.Payload))
	buf[0] = f.Magic[0]
	buf[1] = f.Magic[1]
	buf[2] = uint8(f.Type)
	binary.BigEndian.PutUint64(buf[3:11], f.SequenceID)
	binary.BigEndian.PutUint64(buf[11:19], f.Timestamp)
	buf[19] = f.Encoding
	binary.BigEndian.PutUint32(buf[20:24], f.PayloadLength)
	copy(buf[FrameHeaderSize:], f.Payload)
	return buf, nil
}

func (f *FramePacket) MarshalBinary() ([]byte, error) {
	return f.Serialize()
}

func (f *FramePacket) UnmarshalBinary(data []byte) error {
	parsed, err := DeserializeFrame(data)
	if err != nil {
		return err
	}
	*f = *parsed
	return nil
}

func DeserializeFrame(data []byte) (*FramePacket, error) {
	if len(data) < FrameHeaderSize {
		return nil, fmt.Errorf("%w: need at least %d bytes, got %d", ErrPacketTooShort, FrameHeaderSize, len(data))
	}

	pkt := &FramePacket{
		Magic:         [2]byte{data[0], data[1]},
		Type:          PacketType(data[2]),
		SequenceID:    binary.BigEndian.Uint64(data[3:11]),
		Timestamp:     binary.BigEndian.Uint64(data[11:19]),
		Encoding:      data[19],
		PayloadLength: binary.BigEndian.Uint32(data[20:24]),
	}

	if pkt.Magic != [2]byte{MagicByte0, MagicByte1} {
		return nil, fmt.Errorf("%w: got %#x %#x", ErrInvalidMagic, pkt.Magic[0], pkt.Magic[1])
	}
	if pkt.Type != PacketTypeFrame {
		return nil, fmt.Errorf("%w: expected %#x, got %#x", ErrUnexpectedPacketType, PacketTypeFrame, pkt.Type)
	}
	if pkt.Encoding != EncodingJPEG && pkt.Encoding != EncodingLZ4 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidEncoding, pkt.Encoding)
	}
	if pkt.PayloadLength > MaxFramePayloadSize {
		return nil, ErrPayloadTooLarge
	}

	expectedTotal := FrameHeaderSize + int(pkt.PayloadLength)
	if len(data) != expectedTotal {
		return nil, fmt.Errorf("%w: expected %d total bytes, got %d", ErrPayloadMismatch, expectedTotal, len(data))
	}

	pkt.Payload = make([]byte, pkt.PayloadLength)
	copy(pkt.Payload, data[FrameHeaderSize:expectedTotal])
	return pkt, nil
}

func (f *FramePacket) Encode(w io.Writer) error {
	raw, err := f.Serialize()
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func DecodeFrame(r io.Reader) (*FramePacket, error) {
	var hdr [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	payloadLen := binary.BigEndian.Uint32(hdr[20:24])
	if payloadLen > MaxFramePayloadSize {
		return nil, ErrPayloadTooLarge
	}
	full := make([]byte, FrameHeaderSize+int(payloadLen))
	copy(full[:FrameHeaderSize], hdr[:])
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, full[FrameHeaderSize:]); err != nil {
			return nil, err
		}
	}
	return DeserializeFrame(full)
}
