package hud

import (
	"fmt"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// Action represents a quick-toolbar action triggered by clicking the HUD.
type Action uint8

const (
	ActionNone Action = iota
	ActionToggleStretch
	ActionToggleFullscreen
	ActionCtrlAltDel
	ActionDisconnect
)

type buttonRect struct {
	x, y, w, h float64
	label      string
	action     Action
	danger     bool
	active     bool
}

// Overlay renders the floating HUD bar and routes toolbar clicks.
type Overlay struct {
	fontSource *text.GoTextFaceSource
	collapsed  bool
	hoverX     float64
	hoverY     float64
	barX       float64
	barY       float64
	barW       float64
	barH       float64
	buttons    []buttonRect
	collapseBt buttonRect
}

// NewOverlay creates a floating HUD overlay using fontSource for crisp scaled text.
func NewOverlay(fontSource *text.GoTextFaceSource) *Overlay {
	return &Overlay{fontSource: fontSource}
}

// ConsumedMouse reports whether (mx, my) lies over the visible HUD bar.
func (o *Overlay) ConsumedMouse(mx, my float64) bool {
	return mx >= o.barX && mx <= o.barX+o.barW && my >= o.barY && my <= o.barY+o.barH
}

func (o *Overlay) layout(screenW int, scale float64, stats Stats, stretch, fullscreen bool) {
	if scale < 1 {
		scale = 1
	}
	barH := 34.0 * scale
	o.barY = 10.0 * scale
	o.barH = barH

	if o.collapsed {
		o.barW = 132 * scale
		o.barX = float64(screenW)/2 - o.barW/2
		o.collapseBt = buttonRect{x: o.barX + o.barW - 36*scale, y: o.barY + 4*scale, w: 30 * scale, h: 26 * scale, label: "+"}
		o.buttons = nil
		return
	}

	stretchLbl := "Fit"
	if stretch {
		stretchLbl = "Stretch"
	}
	fullLbl := "Full"
	if fullscreen {
		fullLbl = "Window"
	}

	metricsW := 248.0 * scale
	specs := []struct {
		w      float64
		label  string
		action Action
		danger bool
		active bool
	}{
		{64 * scale, stretchLbl, ActionToggleStretch, false, stretch},
		{64 * scale, fullLbl, ActionToggleFullscreen, false, fullscreen},
		{98 * scale, "Ctrl+Alt+Del", ActionCtrlAltDel, false, false},
		{86 * scale, "Disconnect", ActionDisconnect, true, false},
	}

	gap := 6.0 * scale
	totalBtnW := 0.0
	for _, sp := range specs {
		totalBtnW += sp.w + gap
	}
	o.barW = metricsW + totalBtnW + 34.0*scale
	if o.barW > float64(screenW)-16*scale {
		o.barW = float64(screenW) - 16*scale
	}
	o.barX = float64(screenW)/2 - o.barW/2
	if o.barX < 8*scale {
		o.barX = 8 * scale
	}

	o.buttons = o.buttons[:0]
	cx := o.barX + metricsW
	for _, sp := range specs {
		o.buttons = append(o.buttons, buttonRect{
			x: cx, y: o.barY + 4*scale, w: sp.w, h: 26 * scale,
			label: sp.label, action: sp.action, danger: sp.danger, active: sp.active,
		})
		cx += sp.w + gap
	}
	o.collapseBt = buttonRect{x: o.barX + o.barW - 30*scale, y: o.barY + 4*scale, w: 24 * scale, h: 26 * scale, label: "-"}
	_ = stats
}

// Update processes pointer hover/click against the HUD and returns any triggered Action.
func (o *Overlay) Update(screenW int, scale, mx, my float64, justPressed bool, stats Stats, stretch, fullscreen bool) Action {
	o.hoverX, o.hoverY = mx, my
	o.layout(screenW, scale, stats, stretch, fullscreen)
	if !justPressed {
		return ActionNone
	}
	if hitRect(mx, my, o.collapseBt) {
		o.collapsed = !o.collapsed
		o.layout(screenW, scale, stats, stretch, fullscreen)
		return ActionNone
	}
	for _, b := range o.buttons {
		if hitRect(mx, my, b) {
			return b.action
		}
	}
	return ActionNone
}

// Draw renders the floating translucent HUD over the remote frame.
func (o *Overlay) Draw(dst *ebiten.Image, scale float64, stats Stats, stretch, fullscreen bool) {
	if scale < 1 {
		scale = 1
	}
	sw := dst.Bounds().Dx()
	o.layout(sw, scale, stats, stretch, fullscreen)

	bg := color.RGBA{16, 20, 28, 222}
	border := color.RGBA{56, 68, 88, 230}
	vector.DrawFilledRect(dst, float32(o.barX), float32(o.barY), float32(o.barW), float32(o.barH), bg, false)
	vector.StrokeRect(dst, float32(o.barX), float32(o.barY), float32(o.barW), float32(o.barH), 1, border, false)

	dotCol := tierColor(stats.Tier)
	vector.DrawFilledCircle(dst, float32(o.barX+15*scale), float32(o.barY+o.barH/2), float32(5*scale), dotCol, true)

	pingTxt := "-- ms"
	if stats.HasPing {
		pingTxt = fmt.Sprintf("%d ms", int(math.Round(stats.PingMs)))
	}

	face := &text.GoTextFace{Source: o.fontSource, Size: 13 * scale}
	if o.collapsed {
		o.drawText(dst, pingTxt, o.barX+26*scale, o.barY+9*scale, face, color.RGBA{240, 244, 250, 255})
		o.drawBtn(dst, o.collapseBt, face)
		return
	}

	metricTxt := fmt.Sprintf("%s  |  %2.0f FPS  |  %s", pingTxt, stats.FPS, FormatBandwidth(stats.BytesPerSec))
	o.drawText(dst, metricTxt, o.barX+26*scale, o.barY+9*scale, face, color.RGBA{235, 240, 248, 255})

	btnFace := &text.GoTextFace{Source: o.fontSource, Size: 12 * scale}
	for _, b := range o.buttons {
		o.drawBtn(dst, b, btnFace)
	}
	o.drawBtn(dst, o.collapseBt, btnFace)
}

func (o *Overlay) drawBtn(dst *ebiten.Image, b buttonRect, face text.Face) {
	hovered := hitRect(o.hoverX, o.hoverY, b)
	var bg color.RGBA
	switch {
	case b.danger && hovered:
		bg = color.RGBA{200, 48, 48, 245}
	case b.danger:
		bg = color.RGBA{153, 35, 35, 225}
	case b.active && hovered:
		bg = color.RGBA{59, 130, 246, 245}
	case b.active:
		bg = color.RGBA{37, 99, 235, 230}
	case hovered:
		bg = color.RGBA{65, 78, 100, 240}
	default:
		bg = color.RGBA{40, 49, 64, 230}
	}
	vector.DrawFilledRect(dst, float32(b.x), float32(b.y), float32(b.w), float32(b.h), bg, false)

	tw, th := text.Measure(b.label, face, 0)
	tx := b.x + (b.w-tw)/2
	ty := b.y + (b.h-th)/2
	o.drawText(dst, b.label, tx, ty, face, color.RGBA{245, 248, 255, 255})
}

func (o *Overlay) drawText(dst *ebiten.Image, s string, x, y float64, face text.Face, col color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(col)
	text.Draw(dst, s, face, op)
}

func hitRect(mx, my float64, b buttonRect) bool {
	return mx >= b.x && mx <= b.x+b.w && my >= b.y && my <= b.y+b.h
}

func tierColor(t PingTier) color.RGBA {
	switch t {
	case TierGood:
		return color.RGBA{34, 197, 94, 255}
	case TierWarn:
		return color.RGBA{234, 179, 8, 255}
	case TierBad:
		return color.RGBA{239, 68, 68, 255}
	default:
		return color.RGBA{148, 163, 184, 255}
	}
}
