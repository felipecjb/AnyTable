package protocol

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	ProtocolVersion uint16 = 1

	// AuthPacketSize: Magic(2) + Type(1) + Version(2) + PINHash(32) + Width(2) + Height(2) + Authenticated(1) = 42 bytes
	AuthPacketSize = 42
)

var (
	ErrVersionMismatch   = errors.New("protocol: unsupported protocol version")
	ErrAuthFailed        = errors.New("protocol: SHA256 PIN verification failed")
	ErrInvalidResolution = errors.New("protocol: display resolution width and height must be non-zero")
)

type AuthPacket struct {
	Magic         [2]byte
	Type          PacketType
	Version       uint16
	PINHash       [32]byte
	Width         uint16
	Height        uint16
	Authenticated bool
}

type HandshakePacket = AuthPacket

func HashPIN(pin string) [32]byte {
	return sha256.Sum256([]byte(pin))
}

func VerifyPINHash(expectedHash, candidateHash [32]byte) bool {
	return subtle.ConstantTimeCompare(expectedHash[:], candidateHash[:]) == 1
}

func VerifyPIN(expectedPIN string, candidateHash [32]byte) bool {
	expected := HashPIN(expectedPIN)
	return VerifyPINHash(expected, candidateHash)
}

func NewAuthPacket(pin string, width, height uint16) *AuthPacket {
	return &AuthPacket{
		Magic:         [2]byte{MagicByte0, MagicByte1},
		Type:          PacketTypeAuth,
		Version:       ProtocolVersion,
		PINHash:       HashPIN(pin),
		Width:         width,
		Height:        height,
		Authenticated: false,
	}
}

func NewHandshakePacket(pin string, width, height uint16) *HandshakePacket {
	return NewAuthPacket(pin, width, height)
}

func (a *AuthPacket) CheckVersion() error {
	if a.Version != ProtocolVersion {
		return fmt.Errorf("%w: expected %d, got %d", ErrVersionMismatch, ProtocolVersion, a.Version)
	}
	return nil
}

func (a *AuthPacket) VerifyPIN(expectedPIN string) error {
	if !VerifyPIN(expectedPIN, a.PINHash) {
		return ErrAuthFailed
	}
	return nil
}

func (a *AuthPacket) VerifyHandshake(expectedPIN string) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if err := a.CheckVersion(); err != nil {
		return err
	}
	if err := a.VerifyPIN(expectedPIN); err != nil {
		return err
	}
	return nil
}

func (a *AuthPacket) Validate() error {
	if a.Magic != [2]byte{MagicByte0, MagicByte1} {
		return fmt.Errorf("%w: got %#x %#x", ErrInvalidMagic, a.Magic[0], a.Magic[1])
	}
	if a.Type != PacketTypeAuth {
		return fmt.Errorf("%w: expected %#x, got %#x", ErrUnexpectedPacketType, PacketTypeAuth, a.Type)
	}
	if a.Width == 0 || a.Height == 0 {
		return fmt.Errorf("%w: %dx%d", ErrInvalidResolution, a.Width, a.Height)
	}
	return nil
}

func (a *AuthPacket) Serialize() ([]byte, error) {
	if a.Magic == [2]byte{0, 0} {
		a.Magic = [2]byte{MagicByte0, MagicByte1}
	}
	if a.Type == 0 {
		a.Type = PacketTypeAuth
	}
	if a.Version == 0 {
		a.Version = ProtocolVersion
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}

	var buf [AuthPacketSize]byte
	buf[0] = a.Magic[0]
	buf[1] = a.Magic[1]
	buf[2] = uint8(a.Type)
	binary.BigEndian.PutUint16(buf[3:5], a.Version)
	copy(buf[5:37], a.PINHash[:])
	binary.BigEndian.PutUint16(buf[37:39], a.Width)
	binary.BigEndian.PutUint16(buf[39:41], a.Height)
	if a.Authenticated {
		buf[41] = 1
	} else {
		buf[41] = 0
	}
	return buf[:], nil
}

func (a *AuthPacket) MarshalBinary() ([]byte, error) {
	return a.Serialize()
}

func (a *AuthPacket) UnmarshalBinary(data []byte) error {
	parsed, err := DeserializeAuth(data)
	if err != nil {
		return err
	}
	*a = *parsed
	return nil
}

func DeserializeAuth(data []byte) (*AuthPacket, error) {
	if len(data) < AuthPacketSize {
		return nil, fmt.Errorf("%w: need %d bytes, got %d", ErrPacketTooShort, AuthPacketSize, len(data))
	}
	if len(data) > AuthPacketSize {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrPayloadMismatch, AuthPacketSize, len(data))
	}
	if data[41] > 1 {
		return nil, fmt.Errorf("protocol: invalid authenticated boolean byte %d", data[41])
	}

	pkt := &AuthPacket{
		Magic:         [2]byte{data[0], data[1]},
		Type:          PacketType(data[2]),
		Version:       binary.BigEndian.Uint16(data[3:5]),
		Width:         binary.BigEndian.Uint16(data[37:39]),
		Height:        binary.BigEndian.Uint16(data[39:41]),
		Authenticated: data[41] == 1,
	}
	copy(pkt.PINHash[:], data[5:37])

	if err := pkt.Validate(); err != nil {
		return nil, err
	}
	return pkt, nil
}

func DeserializeHandshake(data []byte) (*HandshakePacket, error) {
	return DeserializeAuth(data)
}

func (a *AuthPacket) Encode(w io.Writer) error {
	raw, err := a.Serialize()
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func DecodeAuth(r io.Reader) (*AuthPacket, error) {
	var buf [AuthPacketSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return nil, err
	}
	return DeserializeAuth(buf[:])
}

func DecodeHandshake(r io.Reader) (*HandshakePacket, error) {
	return DecodeAuth(r)
}
