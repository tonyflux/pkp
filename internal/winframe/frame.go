// Package winframe decides where the main window opens and remembers where
// the user left it. Every figure is in DIPs (CSS pixels), in the global
// top-left-origin space Wails reports screens and windows in.
//
// Only this package sets the main window's bounds: nothing the page does —
// selecting files, opening the right column — resizes the window.
package winframe

// Rect is a window frame or a screen's work area.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Size is a minimum window size.
type Size struct{ Width, Height int }

// Screen is a display as placement needs it.
type Screen struct {
	WorkArea Rect
	Primary  bool
}

// First-launch frame and the floor the window may be shrunk to. The margin keeps a
// default frame off the edges of a small work area.
const (
	defaultWidth  = 1400
	defaultHeight = 900
	screenMargin  = 40
	minWidth      = 960
	minHeight     = 640
)

// Place picks the frame the window opens with and its minimum size. With no
// saved frame it is the default size centred in the primary work area. A
// saved frame goes back where it was, pulled into the work area of the
// screen it overlaps most and shrunk to fit; one that overlaps no screen —
// its monitor is gone — keeps its size but is centred on the primary
// screen. ok is false when there are no screens to place on.
func Place(saved *Rect, screens []Screen) (frame Rect, floor Size, ok bool) {
	if len(screens) == 0 {
		return Rect{}, Size{}, false
	}
	hasSaved := saved != nil && saved.Width > 0 && saved.Height > 0
	if hasSaved {
		if s, found := mostOverlapped(*saved, screens); found {
			floor = minFor(s.WorkArea)
			return clampInto(*saved, s.WorkArea, floor), floor, true
		}
	}
	wa := primary(screens).WorkArea
	floor = minFor(wa)
	width := min(defaultWidth, wa.Width-screenMargin)
	height := min(defaultHeight, wa.Height-screenMargin)
	if hasSaved {
		width, height = saved.Width, saved.Height
	}
	width = clamp(width, floor.Width, wa.Width)
	height = clamp(height, floor.Height, wa.Height)
	return Rect{
		X:     wa.X + (wa.Width-width)/2,
		Y:     wa.Y + (wa.Height-height)/2,
		Width: width, Height: height,
	}, floor, true
}

// minFor is the minimum window size on a work area: the design floor, or
// the whole work area on a screen smaller than that.
func minFor(wa Rect) Size {
	return Size{Width: min(minWidth, wa.Width), Height: min(minHeight, wa.Height)}
}

// clampInto fits r inside wa: no smaller than floor, no larger than wa, and
// moved the least distance needed to lie wholly within it.
func clampInto(r, wa Rect, floor Size) Rect {
	r.Width = clamp(r.Width, floor.Width, wa.Width)
	r.Height = clamp(r.Height, floor.Height, wa.Height)
	r.X = clamp(r.X, wa.X, wa.X+wa.Width-r.Width)
	r.Y = clamp(r.Y, wa.Y, wa.Y+wa.Height-r.Height)
	return r
}

// mostOverlapped is the screen whose work area shares the most area with r;
// found is false when r overlaps none.
func mostOverlapped(r Rect, screens []Screen) (best Screen, found bool) {
	most := 0
	for _, s := range screens {
		if a := overlap(r, s.WorkArea); a > most {
			best, most, found = s, a, true
		}
	}
	return best, found
}

func overlap(a, b Rect) int {
	w := min(a.X+a.Width, b.X+b.Width) - max(a.X, b.X)
	h := min(a.Y+a.Height, b.Y+b.Height) - max(a.Y, b.Y)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

func primary(screens []Screen) Screen {
	for _, s := range screens {
		if s.Primary {
			return s
		}
	}
	return screens[0]
}

// clamp bounds v to [lo, hi]; lo wins when the range is empty.
func clamp(v, lo, hi int) int {
	return max(lo, min(v, hi))
}
