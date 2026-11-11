//go:build linux

package input

import (
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"anytable/common/protocol"
	"anytable/server/src/capture"
)

// linux/uinput.h + input-event-codes.h values (identical on arm, arm64 and amd64).
const (
	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565
	uiSetRelBit  = 0x40045566
	uiSetAbsBit  = 0x40045567
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502

	evSyn = 0x00
	evKey = 0x01
	evRel = 0x02
	evAbs = 0x03

	relWheel = 0x08
	absX     = 0x00
	absY     = 0x01

	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112

	absMax        = 65535
	userDevSize   = 80 + 8 + 4 + 4*64*4 // struct uinput_user_dev
	maxEvdevKey   = 255
	busUSB        = 3
	deviceSettleT = 200 * time.Millisecond
)

type uinputBackend struct {
	kbd, ptr int
}

type evt struct {
	typ, code uint16
	val       int32
}

func newUinput() (Backend, error) {
	kbd, err := createUinputDevice("AnyTable Virtual Keyboard", func(fd int) error {
		if err := unix.IoctlSetInt(fd, uiSetEvBit, evKey); err != nil {
			return err
		}
		for k := 1; k <= maxEvdevKey; k++ {
			if err := unix.IoctlSetInt(fd, uiSetKeyBit, k); err != nil {
				return err
			}
		}
		return nil
	}, false)
	if err != nil {
		return nil, err
	}
	ptr, err := createUinputDevice("AnyTable Virtual Pointer", func(fd int) error {
		for _, ev := range []int{evKey, evAbs, evRel} {
			if err := unix.IoctlSetInt(fd, uiSetEvBit, ev); err != nil {
				return err
			}
		}
		for _, b := range []int{btnLeft, btnRight, btnMiddle} {
			if err := unix.IoctlSetInt(fd, uiSetKeyBit, b); err != nil {
				return err
			}
		}
		for _, a := range []int{absX, absY} {
			if err := unix.IoctlSetInt(fd, uiSetAbsBit, a); err != nil {
				return err
			}
		}
		return unix.IoctlSetInt(fd, uiSetRelBit, relWheel)
	}, true)
	if err != nil {
		destroyUinput(kbd)
		return nil, err
	}
	time.Sleep(deviceSettleT) // let udev/X pick the new devices up before the first event
	return &uinputBackend{kbd: kbd, ptr: ptr}, nil
}

func openUinput() (int, error) {
	var last error
	for _, p := range []string{"/dev/uinput", "/dev/input/uinput"} {
		fd, err := unix.Open(p, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err == nil {
			return fd, nil
		}
		last = err
	}
	return -1, last
}

func createUinputDevice(name string, setup func(fd int) error, abs bool) (int, error) {
	fd, err := openUinput()
	if err != nil {
		return -1, err
	}
	fail := func(err error) (int, error) {
		_ = unix.Close(fd)
		return -1, err
	}
	if err := setup(fd); err != nil {
		return fail(err)
	}
	dev := make([]byte, userDevSize)
	copy(dev, name)
	binary.LittleEndian.PutUint16(dev[80:], busUSB)
	binary.LittleEndian.PutUint16(dev[82:], 0x1209) // vendor
	binary.LittleEndian.PutUint16(dev[84:], 0x7a11) // product
	binary.LittleEndian.PutUint16(dev[86:], 1)      // version
	if abs {
		const absmaxOff = 92 // after name[80], input_id[8], ff_effects_max[4]
		binary.LittleEndian.PutUint32(dev[absmaxOff+4*absX:], absMax)
		binary.LittleEndian.PutUint32(dev[absmaxOff+4*absY:], absMax)
	}
	if _, err := unix.Write(fd, dev); err != nil {
		return fail(err)
	}
	if err := unix.IoctlSetInt(fd, uiDevCreate, 0); err != nil {
		return fail(err)
	}
	return fd, nil
}

func destroyUinput(fd int) {
	_ = unix.IoctlSetInt(fd, uiDevDestroy, 0)
	_ = unix.Close(fd)
}

// emit writes the events followed by SYN_REPORT in one syscall.
func emit(fd int, evs ...evt) error {
	tv := int(unsafe.Sizeof(syscall.Timeval{})) // struct input_event starts with a timeval (kernel stamps it)
	sz := tv + 8
	buf := make([]byte, sz*(len(evs)+1))
	put := func(i int, e evt) {
		o := i*sz + tv
		binary.LittleEndian.PutUint16(buf[o:], e.typ)
		binary.LittleEndian.PutUint16(buf[o+2:], e.code)
		binary.LittleEndian.PutUint32(buf[o+4:], uint32(e.val))
	}
	for i, e := range evs {
		put(i, e)
	}
	put(len(evs), evt{typ: evSyn})
	_, err := unix.Write(fd, buf)
	return err
}

func (u *uinputBackend) Name() string { return "uinput" }

func (u *uinputBackend) Close() error {
	destroyUinput(u.ptr)
	destroyUinput(u.kbd)
	return nil
}

func absValue(px, rootSize int) int32 {
	if rootSize <= 1 {
		return 0
	}
	v := px * absMax / (rootSize - 1)
	if v < 0 {
		v = 0
	}
	if v > absMax {
		v = absMax
	}
	return int32(v)
}

func (u *uinputBackend) MoveTo(r capture.Region, px, py int) error {
	return emit(u.ptr,
		evt{evAbs, absX, absValue(px, r.RootW)},
		evt{evAbs, absY, absValue(py, r.RootH)})
}

func (u *uinputBackend) Button(b protocol.MouseButton, down bool) error {
	var code uint16
	switch b {
	case protocol.MouseButtonLeft:
		code = btnLeft
	case protocol.MouseButtonRight:
		code = btnRight
	case protocol.MouseButtonMiddle:
		code = btnMiddle
	default:
		return errors.New("uinput: unknown mouse button")
	}
	return emit(u.ptr, evt{evKey, code, boolVal(down)})
}

func (u *uinputBackend) Wheel(notches int) error {
	return emit(u.ptr, evt{evRel, relWheel, int32(notches)})
}

func (u *uinputBackend) Key(code uint16, down bool) error {
	if code < 1 || code > maxEvdevKey {
		return fmt.Errorf("%w: %d", ErrBadKey, code)
	}
	return emit(u.kbd, evt{evKey, code, boolVal(down)})
}

func boolVal(b bool) int32 {
	if b {
		return 1
	}
	return 0
}
