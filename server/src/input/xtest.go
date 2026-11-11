package input

import (
	"errors"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"

	"anytable/common/protocol"
	"anytable/server/src/capture"
)

const maxWheelNotches = 20

// xtestBackend injects through the XTest extension (XTestFakeKeyEvent / MotionEvent equivalents).
type xtestBackend struct {
	conn *xgb.Conn
	root xproto.Window
}

func newXTest(conn *xgb.Conn) (Backend, error) {
	if conn == nil {
		return nil, errors.New("xtest: no X connection")
	}
	if err := xtest.Init(conn); err != nil {
		return nil, err
	}
	return &xtestBackend{conn: conn, root: xproto.Setup(conn).DefaultScreen(conn).Root}, nil
}

func (x *xtestBackend) Name() string { return "xtest" }

func (x *xtestBackend) Close() error { return nil }

func (x *xtestBackend) fake(typ, detail byte, px, py int) {
	xtest.FakeInput(x.conn, typ, detail, 0, x.root, int16(px), int16(py), 0)
}

func (x *xtestBackend) MoveTo(_ capture.Region, px, py int) error {
	x.fake(xproto.MotionNotify, 0, px, py)
	return nil
}

func (x *xtestBackend) Button(b protocol.MouseButton, down bool) error {
	var detail byte
	switch b {
	case protocol.MouseButtonLeft:
		detail = 1
	case protocol.MouseButtonMiddle:
		detail = 2
	case protocol.MouseButtonRight:
		detail = 3
	default:
		return nil
	}
	typ := byte(xproto.ButtonRelease)
	if down {
		typ = xproto.ButtonPress
	}
	x.fake(typ, detail, 0, 0)
	return nil
}

func (x *xtestBackend) Wheel(notches int) error {
	detail := byte(4) // button 4 = scroll up, 5 = down
	if notches < 0 {
		detail = 5
		notches = -notches
	}
	if notches > maxWheelNotches {
		notches = maxWheelNotches
	}
	for n := 0; n < notches; n++ {
		x.fake(xproto.ButtonPress, detail, 0, 0)
		x.fake(xproto.ButtonRelease, detail, 0, 0)
	}
	return nil
}

// Key: X11 keycodes under the evdev driver equal the Linux evdev code + 8.
func (x *xtestBackend) Key(code uint16, down bool) error {
	if code+8 > 255 {
		return ErrBadKey
	}
	typ := byte(xproto.KeyRelease)
	if down {
		typ = xproto.KeyPress
	}
	x.fake(typ, byte(code+8), 0, 0)
	return nil
}
