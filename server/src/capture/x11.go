package capture

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/shm"
	"github.com/jezek/xgb/xproto"
)

const (
	requestTimeout = 3 * time.Second
	regionRefresh  = 2 * time.Second
	maxShmFailures = 3
	allPlanes      = 0xFFFFFFFF
)

type shmSegment struct {
	id   int
	mem  []byte
	xseg shm.Seg
}

// X11Grabber captures the primary monitor of an X display using MIT-SHM
// (falling back to plain GetImage). It is driven by a single goroutine (Grab/Close);
// Region is safe from any goroutine.
type X11Grabber struct {
	conn     *xgb.Conn
	root     xproto.Window
	bgr      bool
	hasRandr bool
	pool     sync.Pool

	mu     sync.RWMutex
	region Region

	lastRefresh time.Time
	dirty       bool
	seq         uint64

	useShm   bool
	seg      *shmSegment
	shmFails int
}

// NewX11 validates the display pixel format and detects the primary monitor.
func NewX11(conn *xgb.Conn) (*X11Grabber, error) {
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	bgr, err := checkFormat(setup, screen)
	if err != nil {
		return nil, err
	}
	g := &X11Grabber{conn: conn, root: screen.Root, bgr: bgr, useShm: true}
	g.hasRandr = randr.Init(conn) == nil
	if err := g.Refresh(); err != nil {
		return nil, err
	}
	return g, nil
}

func checkFormat(setup *xproto.SetupInfo, screen *xproto.ScreenInfo) (bool, error) {
	if setup.ImageByteOrder != xproto.ImageOrderLSBFirst {
		return false, errors.New("capture: only little-endian X images are supported")
	}
	var bpp byte
	for _, f := range setup.PixmapFormats {
		if f.Depth == screen.RootDepth {
			bpp = f.BitsPerPixel
		}
	}
	if bpp != 32 {
		return false, fmt.Errorf("capture: unsupported root depth %d with %d bits per pixel (need 32)", screen.RootDepth, bpp)
	}
	for _, d := range screen.AllowedDepths {
		for _, v := range d.Visuals {
			if v.VisualId != screen.RootVisual {
				continue
			}
			switch {
			case v.RedMask == 0xff0000 && v.GreenMask == 0xff00 && v.BlueMask == 0xff:
				return true, nil
			case v.RedMask == 0xff && v.GreenMask == 0xff00 && v.BlueMask == 0xff0000:
				return false, nil
			}
		}
	}
	return false, errors.New("capture: unsupported root visual channel masks")
}

// Region returns the current capture rectangle.
func (g *X11Grabber) Region() Region {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.region
}

// Refresh re-queries the monitor geometry and re-creates the shared segment if the size changed.
func (g *X11Grabber) Refresh() error {
	r, err := g.queryRegion()
	if err != nil {
		return err
	}
	if r.W <= 0 || r.H <= 0 {
		return errors.New("capture: empty display geometry")
	}
	g.mu.Lock()
	g.region = r
	g.mu.Unlock()
	g.lastRefresh = time.Now()
	g.dirty = false
	if g.useShm {
		need := r.W * r.H * 4
		if g.seg == nil || len(g.seg.mem) != need {
			g.resetShm(need)
		}
	}
	return nil
}

func (g *X11Grabber) queryRegion() (Region, error) {
	geo, err := call(requestTimeout, func() (*xproto.GetGeometryReply, error) {
		return xproto.GetGeometry(g.conn, xproto.Drawable(g.root)).Reply()
	})
	if err != nil {
		return Region{}, err
	}
	r := Region{W: int(geo.Width), H: int(geo.Height), RootW: int(geo.Width), RootH: int(geo.Height)}
	if g.hasRandr {
		if x, y, w, h, ok := g.primaryRect(); ok && x >= 0 && y >= 0 && x+w <= r.RootW && y+h <= r.RootH {
			r.X, r.Y, r.W, r.H = x, y, w, h
		}
	}
	return r, nil
}

type rect struct{ x, y, w, h int }

// primaryRect returns the primary output's CRTC rectangle, or the first active CRTC when no primary is set.
func (g *X11Grabber) primaryRect() (x, y, w, h int, ok bool) {
	rc, err := call(requestTimeout, func() (rect, error) {
		res, err := randr.GetScreenResources(g.conn, g.root).Reply()
		if err != nil {
			return rect{}, err
		}
		prim, err := randr.GetOutputPrimary(g.conn, g.root).Reply()
		if err == nil && prim.Output != 0 {
			oi, err := randr.GetOutputInfo(g.conn, prim.Output, res.ConfigTimestamp).Reply()
			if err == nil && oi.Crtc != 0 {
				ci, err := randr.GetCrtcInfo(g.conn, oi.Crtc, res.ConfigTimestamp).Reply()
				if err == nil && ci.Width > 0 && ci.Height > 0 {
					return rect{int(ci.X), int(ci.Y), int(ci.Width), int(ci.Height)}, nil
				}
			}
		}
		for _, crtc := range res.Crtcs {
			ci, err := randr.GetCrtcInfo(g.conn, crtc, res.ConfigTimestamp).Reply()
			if err == nil && ci.Width > 0 && ci.Height > 0 && ci.Mode != 0 {
				return rect{int(ci.X), int(ci.Y), int(ci.Width), int(ci.Height)}, nil
			}
		}
		return rect{}, errors.New("capture: no active CRTC")
	})
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return rc.x, rc.y, rc.w, rc.h, true
}

func (g *X11Grabber) resetShm(size int) {
	g.closeShm()
	if err := shm.Init(g.conn); err != nil {
		g.useShm = false
		return
	}
	id, mem, err := shmAlloc(size)
	if err != nil {
		g.useShm = false
		return
	}
	xseg, err := shm.NewSegId(g.conn)
	if err == nil {
		_, err = call(requestTimeout, func() (struct{}, error) {
			return struct{}{}, shm.AttachChecked(g.conn, xseg, uint32(id), false).Check()
		})
	}
	// Mark for removal now: the segment disappears once both sides detach.
	shmRemove(id)
	if err != nil {
		shmDetach(mem)
		g.useShm = false // X server cannot access our segment (e.g. different uid); use GetImage.
		return
	}
	g.seg = &shmSegment{id: id, mem: mem, xseg: xseg}
}

func (g *X11Grabber) closeShm() {
	if g.seg == nil {
		return
	}
	shm.Detach(g.conn, g.seg.xseg)
	shmDetach(g.seg.mem)
	g.seg = nil
}

// Close releases the shared memory segment. The X connection is owned by the caller.
func (g *X11Grabber) Close() { g.closeShm() }

// Mode reports the capture path in use ("shm" or "getimage").
func (g *X11Grabber) Mode() string {
	if g.seg != nil {
		return "shm"
	}
	return "getimage"
}

func (g *X11Grabber) getBuf(n int) []byte {
	if b, ok := g.pool.Get().([]byte); ok && cap(b) >= n {
		return b[:n]
	}
	return make([]byte, n)
}

func (g *X11Grabber) putBuf(b []byte) { g.pool.Put(b[:cap(b)]) }

// Grab captures one frame of the primary monitor.
func (g *X11Grabber) Grab() (*Frame, error) {
	if g.dirty || time.Since(g.lastRefresh) >= regionRefresh {
		if err := g.Refresh(); err != nil {
			g.dirty = true
			return nil, err
		}
	}
	r := g.Region()
	n := r.W * r.H * 4
	var f *Frame
	var err error
	if g.seg != nil {
		f, err = g.grabShm(r, n)
	} else {
		f, err = g.grabImage(r, n)
	}
	if err != nil {
		g.dirty = true
		return nil, err
	}
	g.seq++
	f.Seq = g.seq
	return f, nil
}

func (g *X11Grabber) grabShm(r Region, n int) (*Frame, error) {
	seg := g.seg
	rep, err := call(requestTimeout, func() (*shm.GetImageReply, error) {
		return shm.GetImage(g.conn, xproto.Drawable(g.root), int16(r.X), int16(r.Y), uint16(r.W), uint16(r.H),
			allPlanes, xproto.ImageFormatZPixmap, seg.xseg, 0).Reply()
	})
	if err == nil && (int(rep.Size) != n || n > len(seg.mem)) {
		err = fmt.Errorf("capture: unexpected shm image size %d, want %d", rep.Size, n)
	}
	if err != nil {
		g.shmFails++
		if g.shmFails >= maxShmFailures {
			g.closeShm()
			g.useShm = false
		}
		return nil, err
	}
	g.shmFails = 0
	buf := g.getBuf(n)
	copy(buf, seg.mem[:n])
	return NewFrame(buf, r.W, r.H, g.bgr, g.putBuf), nil
}

func (g *X11Grabber) grabImage(r Region, n int) (*Frame, error) {
	rep, err := call(requestTimeout, func() (*xproto.GetImageReply, error) {
		return xproto.GetImage(g.conn, xproto.ImageFormatZPixmap, xproto.Drawable(g.root),
			int16(r.X), int16(r.Y), uint16(r.W), uint16(r.H), allPlanes).Reply()
	})
	if err != nil {
		return nil, err
	}
	if len(rep.Data) != n {
		return nil, fmt.Errorf("capture: unexpected image size %d, want %d", len(rep.Data), n)
	}
	return NewFrame(rep.Data, r.W, r.H, g.bgr, nil), nil
}
