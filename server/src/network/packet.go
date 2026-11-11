package network

import (
	"bytes"
	"fmt"
	"io"

	"anytable/common/protocol"
)

// packet is one decoded client packet; exactly one field is set.
type packet struct {
	ping  *protocol.PingPacket // PING or PONG
	input *protocol.InputPacket
	auth  *protocol.AuthPacket
}

// readPacket reads and decodes the next framed packet. The wire format is self-delimiting:
// every packet starts with magic(2)+type(1) and the type fixes the remaining length.
func readPacket(r io.Reader) (*packet, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	full := io.MultiReader(bytes.NewReader(hdr[:]), r)
	switch protocol.PacketType(hdr[2]) {
	case protocol.PacketTypePing, protocol.PacketTypePong:
		p, err := protocol.DecodePing(full)
		return &packet{ping: p}, err
	case protocol.PacketTypeInputMouse, protocol.PacketTypeInputKeyboard:
		p, err := protocol.DecodeInput(full)
		return &packet{input: p}, err
	case protocol.PacketTypeAuth:
		p, err := protocol.DecodeAuth(full)
		return &packet{auth: p}, err
	default:
		return nil, fmt.Errorf("network: unexpected packet type %#x", hdr[2])
	}
}
