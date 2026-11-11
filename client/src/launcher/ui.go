package launcher

import (
	"image/color"
	"strconv"
	"strings"
	"unicode"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// ConnectRequest holds validated connection parameters submitted by the launcher.
type ConnectRequest struct {
	Host string
	Port int
	PIN  string
}

type fieldID int

const (
	fieldHost fieldID = iota
	fieldPort
	fieldPIN
)

type box struct {
	x, y, w, h float64
}

func (b box) contains(mx, my float64) bool {
	return mx >= b.x && mx <= b.x+b.w && my >= b.y && my <= b.y+b.h
}

// UI renders and handles input for the connection launcher screen.
type UI struct {
	fontSource *text.GoTextFaceSource
	store      *Store

	host    string
	port    string
	pin     string
	showPIN bool
	focused fieldID

	dropdownOpen bool
	connecting   bool
	statusMsg    string
	statusIsErr  bool

	charsBuf []rune
	hoverX   float64
	hoverY   float64
}

// NewUI creates a launcher UI backed by store.
func NewUI(fontSource *text.GoTextFaceSource, store *Store) *UI {
	u := &UI{
		fontSource: fontSource,
		store:      store,
		port:       strconv.Itoa(DefaultPort),
		focused:    fieldHost,
	}
	if entries := store.Entries(); len(entries) > 0 {
		u.host = entries[0].Host
		u.port = strconv.Itoa(entries[0].Port)
	}
	return u
}

// SetPrefill sets host/port/pin values programmatically (e.g. from CLI flags).
func (u *UI) SetPrefill(host string, port int, pin string) {
	if host != "" {
		u.host = host
	}
	if port > 0 {
		u.port = strconv.Itoa(port)
	}
	if pin != "" {
		u.pin = pin
	}
}

// SetStatus updates the launcher status line and connecting state.
func (u *UI) SetStatus(msg string, isErr, connecting bool) {
	u.statusMsg = msg
	u.statusIsErr = isErr
	u.connecting = connecting
}

type layoutBoxes struct {
	card     box
	recent   box
	dropList box
	dropRows []box
	host     box
	port     box
	pin      box
	pinEye   box
	connect  box
}

func (u *UI) computeLayout(w, h int, scale float64) layoutBoxes {
	if scale < 1 {
		scale = 1
	}
	cw := 440.0 * scale
	ch := 390.0 * scale
	if cw > float64(w)-32*scale {
		cw = float64(w) - 32*scale
	}
	cx := (float64(w) - cw) / 2
	cy := (float64(h) - ch) / 2
	if cy < 20*scale {
		cy = 20 * scale
	}
	pad := 28.0 * scale
	innerW := cw - pad*2
	ix := cx + pad

	recent := box{ix, cy + 92*scale, innerW, 34 * scale}
	host := box{ix, cy + 156*scale, innerW*0.68 - 10*scale, 36 * scale}
	port := box{host.x + host.w + 10*scale, cy + 156*scale, innerW * 0.32, 36 * scale}
	pin := box{ix, cy + 222*scale, innerW - 64*scale, 36 * scale}
	pinEye := box{pin.x + pin.w + 8*scale, cy + 222*scale, 56 * scale, 36 * scale}
	connect := box{ix, cy + 282*scale, innerW, 40 * scale}

	entries := u.store.Entries()
	rowH := 30.0 * scale
	rows := make([]box, len(entries))
	for i := range entries {
		rows[i] = box{recent.x, recent.y + recent.h + 2*scale + float64(i)*rowH, recent.w, rowH}
	}
	dropList := box{recent.x, recent.y + recent.h + 2*scale, recent.w, float64(len(entries)) * rowH}

	return layoutBoxes{
		card:     box{cx, cy, cw, ch},
		recent:   recent,
		dropList: dropList,
		dropRows: rows,
		host:     host,
		port:     port,
		pin:      pin,
		pinEye:   pinEye,
		connect:  connect,
	}
}

// Update processes keyboard/mouse input and returns a non-nil ConnectRequest when Connect is triggered.
func (u *UI) Update(w, h int, scale float64) *ConnectRequest {
	imx, imy := ebiten.CursorPosition()
	mx, my := float64(imx), float64(imy)
	u.hoverX, u.hoverY = mx, my
	lb := u.computeLayout(w, h, scale)
	clicked := inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)

	if u.dropdownOpen {
		if clicked {
			entries := u.store.Entries()
			for i, rb := range lb.dropRows {
				if rb.contains(mx, my) && i < len(entries) {
					u.host = entries[i].Host
					u.port = strconv.Itoa(entries[i].Port)
					u.statusMsg = ""
					u.statusIsErr = false
					u.dropdownOpen = false
					return nil
				}
			}
			u.dropdownOpen = false
			if lb.recent.contains(mx, my) {
				return nil
			}
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
			u.dropdownOpen = false
		}
		return nil
	}

	if clicked && !u.connecting {
		switch {
		case lb.recent.contains(mx, my):
			if len(u.store.Entries()) > 0 {
				u.dropdownOpen = true
			}
		case lb.host.contains(mx, my):
			u.focused = fieldHost
		case lb.port.contains(mx, my):
			u.focused = fieldPort
		case lb.pin.contains(mx, my):
			u.focused = fieldPIN
		case lb.pinEye.contains(mx, my):
			u.showPIN = !u.showPIN
		case lb.connect.contains(mx, my):
			return u.submit()
		}
	}

	if u.connecting {
		return nil
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		if ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight) {
			u.focused = (u.focused + 2) % 3
		} else {
			u.focused = (u.focused + 1) % 3
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter) {
		return u.submit()
	}

	u.handleTextInput()
	return nil
}

func (u *UI) handleTextInput() {
	u.charsBuf = ebiten.AppendInputChars(u.charsBuf[:0])
	for _, r := range u.charsBuf {
		if unicode.IsControl(r) {
			continue
		}
		switch u.focused {
		case fieldHost:
			if len(u.host) < 128 && !unicode.IsSpace(r) {
				u.host += string(r)
			}
		case fieldPort:
			if len(u.port) < 5 && r >= '0' && r <= '9' {
				u.port += string(r)
			}
		case fieldPIN:
			if len(u.pin) < 32 {
				u.pin += string(r)
			}
		}
	}

	if repeatingKey(ebiten.KeyBackspace) {
		switch u.focused {
		case fieldHost:
			u.host = popLastRune(u.host)
		case fieldPort:
			u.port = popLastRune(u.port)
		case fieldPIN:
			u.pin = popLastRune(u.pin)
		}
	}
}

func repeatingKey(k ebiten.Key) bool {
	d := inpututil.KeyPressDuration(k)
	return d == 1 || (d > 24 && d%3 == 0)
}

func popLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[:len(r)-1])
}

func (u *UI) submit() *ConnectRequest {
	host, port, err := Validate(u.host, u.port)
	if err != nil {
		u.SetStatus(err.Error(), true, false)
		return nil
	}
	u.host = host
	u.port = strconv.Itoa(port)
	u.SetStatus("Connecting to "+host+":"+u.port+"...", false, true)
	return &ConnectRequest{Host: host, Port: port, PIN: u.pin}
}

// Draw renders the launcher dialog.
func (u *UI) Draw(dst *ebiten.Image, scale float64) {
	if scale < 1 {
		scale = 1
	}
	dst.Fill(color.RGBA{13, 17, 23, 255})
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	lb := u.computeLayout(w, h, scale)

	// Card background & border
	drawBox(dst, lb.card, color.RGBA{22, 27, 36, 255}, color.RGBA{48, 58, 76, 255})

	titleFace := &text.GoTextFace{Source: u.fontSource, Size: 22 * scale}
	subFace := &text.GoTextFace{Source: u.fontSource, Size: 12 * scale}
	lblFace := &text.GoTextFace{Source: u.fontSource, Size: 12 * scale}
	valFace := &text.GoTextFace{Source: u.fontSource, Size: 14 * scale}

	u.drawText(dst, "AnyTable Viewer", lb.card.x+28*scale, lb.card.y+24*scale, titleFace, color.RGBA{244, 247, 252, 255})
	u.drawText(dst, "Ultra-low-latency LAN remote desktop", lb.card.x+28*scale, lb.card.y+52*scale, subFace, color.RGBA{148, 163, 184, 255})

	// Recent Connections dropdown trigger
	u.drawText(dst, "Recent Connections", lb.recent.x, lb.recent.y-17*scale, lblFace, color.RGBA{176, 188, 206, 255})
	recentLabel := "No saved connections"
	entries := u.store.Entries()
	if len(entries) > 0 {
		recentLabel = "Select recent (" + entries[0].Label() + ")  v"
	}
	drawBox(dst, lb.recent, color.RGBA{30, 37, 50, 255}, color.RGBA{64, 78, 102, 255})
	u.drawText(dst, recentLabel, lb.recent.x+10*scale, lb.recent.y+9*scale, valFace, color.RGBA{203, 213, 225, 255})

	// Host & Port fields
	u.drawText(dst, "Host IP", lb.host.x, lb.host.y-17*scale, lblFace, color.RGBA{176, 188, 206, 255})
	u.drawInput(dst, lb.host, u.host, "192.168.1.50", u.focused == fieldHost, scale, valFace)

	u.drawText(dst, "Port", lb.port.x, lb.port.y-17*scale, lblFace, color.RGBA{176, 188, 206, 255})
	u.drawInput(dst, lb.port, u.port, "8443", u.focused == fieldPort, scale, valFace)

	// PIN field + Show/Hide button
	u.drawText(dst, "PIN (optional)", lb.pin.x, lb.pin.y-17*scale, lblFace, color.RGBA{176, 188, 206, 255})
	pinDisplay := u.pin
	if !u.showPIN && len(u.pin) > 0 {
		pinDisplay = strings.Repeat("*", len([]rune(u.pin)))
	}
	u.drawInput(dst, lb.pin, pinDisplay, "Leave blank if unset", u.focused == fieldPIN, scale, valFace)

	eyeLbl := "Show"
	if u.showPIN {
		eyeLbl = "Hide"
	}
	drawBox(dst, lb.pinEye, color.RGBA{42, 51, 68, 255}, color.RGBA{72, 86, 110, 255})
	u.drawCentered(dst, lb.pinEye, eyeLbl, lblFace, color.RGBA{226, 232, 240, 255})

	// Connect button
	btnBg := color.RGBA{37, 99, 235, 255}
	btnTxt := "Connect"
	if u.connecting {
		btnBg = color.RGBA{30, 64, 140, 255}
		btnTxt = "Connecting..."
	} else if lb.connect.contains(u.hoverX, u.hoverY) {
		btnBg = color.RGBA{59, 130, 246, 255}
	}
	drawBox(dst, lb.connect, btnBg, color.RGBA{96, 165, 250, 200})
	u.drawCentered(dst, lb.connect, btnTxt, &text.GoTextFace{Source: u.fontSource, Size: 15 * scale}, color.RGBA{255, 255, 255, 255})

	// Status line
	if u.statusMsg != "" {
		col := color.RGBA{147, 197, 253, 255}
		if u.statusIsErr {
			col = color.RGBA{248, 113, 113, 255}
		}
		u.drawText(dst, u.statusMsg, lb.connect.x, lb.connect.y+lb.connect.h+14*scale, lblFace, col)
	}

	// Render dropdown overlay last so it appears above inputs
	if u.dropdownOpen && len(entries) > 0 {
		drawBox(dst, lb.dropList, color.RGBA{26, 33, 45, 255}, color.RGBA{96, 165, 250, 255})
		for i, rb := range lb.dropRows {
			if rb.contains(u.hoverX, u.hoverY) {
				vector.DrawFilledRect(dst, float32(rb.x+1), float32(rb.y+1), float32(rb.w-2), float32(rb.h-2), color.RGBA{45, 65, 98, 255}, false)
			}
			u.drawText(dst, entries[i].Label(), rb.x+10*scale, rb.y+7*scale, valFace, color.RGBA{240, 244, 250, 255})
		}
	}
}

func (u *UI) drawInput(dst *ebiten.Image, b box, val, placeholder string, focused bool, scale float64, face text.Face) {
	border := color.RGBA{64, 78, 102, 255}
	if focused {
		border = color.RGBA{59, 130, 246, 255}
	}
	drawBox(dst, b, color.RGBA{15, 20, 28, 255}, border)

	display := val
	col := color.RGBA{241, 245, 249, 255}
	if display == "" {
		display = placeholder
		col = color.RGBA{100, 116, 139, 255}
	}
	if focused {
		if val == "" {
			display = "|"
			col = color.RGBA{241, 245, 249, 255}
		} else {
			display += "|"
		}
	}
	u.drawText(dst, display, b.x+10*scale, b.y+9*scale, face, col)
}

func drawBox(dst *ebiten.Image, b box, bg, border color.RGBA) {
	vector.DrawFilledRect(dst, float32(b.x), float32(b.y), float32(b.w), float32(b.h), bg, false)
	vector.StrokeRect(dst, float32(b.x), float32(b.y), float32(b.w), float32(b.h), 1, border, false)
}

func (u *UI) drawText(dst *ebiten.Image, s string, x, y float64, face text.Face, col color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(col)
	text.Draw(dst, s, face, op)
}

func (u *UI) drawCentered(dst *ebiten.Image, b box, s string, face text.Face, col color.Color) {
	tw, th := text.Measure(s, face, 0)
	u.drawText(dst, s, b.x+(b.w-tw)/2, b.y+(b.h-th)/2, face, col)
}
