package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	// PingPacketSize: Magic(2) + Type(1) + SequenceID(8) + Timestamp(8) = 19 bytes (< 32 bytes)
	PingPacketSize = 19
)

type PingPacket struct {
	Magic      [2]byte
	Type       PacketType
	SequenceID uint64
	Timestamp  uint64
}

func NewPingPacket(seqID uint64) *PingPacket {
	return &PingPacket{
		Magic:      [2]byte{MagicByte0, MagicByte1},
		Type:       PacketTypePing,
		SequenceID: seqID,
		Timestamp:  NowMicroseconds(),
	}
}

func NewPongPacket(seqID uint64, sentTimestamp uint64) *PingPacket {
	return &PingPacket{
		Magic:      [2]byte{MagicByte0, MagicByte1},
		Type:       PacketTypePong,
		SequenceID: seqID,
		Timestamp:  sentTimestamp,
	}
}

func CalculateRTT(sentTimestamp uint64) time.Duration {
	now := NowMicroseconds()
	if now <= sentTimestamp {
		return 0
	}
	return time.Duration(now-sentTimestamp) * time.Microsecond
}

func (p *PingPacket) Validate() error {
	if p.Magic != [2]byte{MagicByte0, MagicByte1} {
		return fmt.Errorf("%w: got %#x %#x", ErrInvalidMagic, p.Magic[0], p.Magic[1])
	}
	if p.Type != PacketTypePing && p.Type != PacketTypePong {
		return fmt.Errorf("%w: expected PING(%#x) or PONG(%#x), got %#x",
			ErrUnexpectedPacketType, PacketTypePing, PacketTypePong, p.Type)
	}
	return nil
}

func (p *PingPacket) Serialize() ([]byte, error) {
	if p.Magic == [2]byte{0, 0} {
		p.Magic = [2]byte{MagicByte0, MagicByte1}
	}
	if p.Type == 0 {
		p.Type = PacketTypePing
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}

	var buf [PingPacketSize]byte
	buf[0] = p.Magic[0]
	buf[1] = p.Magic[1]
	buf[2] = uint8(p.Type)
	binary.BigEndian.PutUint64(buf[3:11], p.SequenceID)
	binary.BigEndian.PutUint64(buf[11:19], p.Timestamp)
	return buf[:], nil
}

func (p *PingPacket) MarshalBinary() ([]byte, error) {
	return p.Serialize()
}

func (p *PingPacket) UnmarshalBinary(data []byte) error {
	parsed, err := DeserializePing(data)
	if err != nil {
		return err
	}
	*p = *parsed
	return nil
}

func DeserializePing(data []byte) (*PingPacket, error) {
	if len(data) < PingPacketSize {
		return nil, fmt.Errorf("%w: need %d bytes, got %d", ErrPacketTooShort, PingPacketSize, len(data))
	}
	if len(data) > PingPacketSize {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrPayloadMismatch, PingPacketSize, len(data))
	}

	pkt := &PingPacket{
		Magic:      [2]byte{data[0], data[1]},
		Type:       PacketType(data[2]),
		SequenceID: binary.BigEndian.Uint64(data[3:11]),
		Timestamp:  binary.BigEndian.Uint64(data[11:19]),
	}
	if err := pkt.Validate(); err != nil {
		return nil, err
	}
	return pkt, nil
}

func (p *PingPacket) Encode(w io.Writer) error {
	raw, err := p.Serialize()
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func DecodePing(r io.Reader) (*PingPacket, error) {
	var buf [PingPacketSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return nil, err
	}
	return DeserializePing(buf[:])
}
