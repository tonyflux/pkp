package winframe

import (
	"path/filepath"
	"testing"
)

func TestPlace(t *testing.T) {
	// A 1512×982 MacBook: the menu bar takes 33 at the top.
	laptop := Screen{WorkArea: Rect{X: 0, Y: 33, Width: 1512, Height: 949}, Primary: true}
	// A 2560×1440 monitor to the right, with its own menu bar.
	monitor := Screen{WorkArea: Rect{X: 1512, Y: -200, Width: 2560, Height: 1415}}
	small := Screen{WorkArea: Rect{X: 0, Y: 25, Width: 900, Height: 600}, Primary: true}
	saved := func(x, y, w, h int) *Rect { return &Rect{X: x, Y: y, Width: w, Height: h} }

	for _, tc := range []struct {
		name    string
		saved   *Rect
		screens []Screen
		want    Rect
		floor   Size
	}{
		{
			name:    "first launch fits",
			screens: []Screen{laptop, monitor},
			want:    Rect{X: (1512 - 1400) / 2, Y: 33 + (949-900)/2, Width: 1400, Height: 900},
			floor:   Size{960, 640},
		},
		{
			name:    "first launch on a large primary keeps the default",
			screens: []Screen{{WorkArea: Rect{Y: 25, Width: 2560, Height: 1415}, Primary: true}},
			want:    Rect{X: 580, Y: 25 + 257, Width: 1400, Height: 900},
			floor:   Size{960, 640},
		},
		{
			name:    "first launch on a screen smaller than the minimum",
			screens: []Screen{small},
			want:    Rect{X: 0, Y: 25, Width: 900, Height: 600},
			floor:   Size{900, 600},
		},
		{
			name:    "saved frame that fits comes back unchanged",
			saved:   saved(2000, 100, 1200, 800),
			screens: []Screen{laptop, monitor},
			want:    Rect{X: 2000, Y: 100, Width: 1200, Height: 800},
			floor:   Size{960, 640},
		},
		{
			name:    "saved frame too big for its screen shrinks into the work area",
			saved:   saved(-50, 0, 2000, 1200),
			screens: []Screen{laptop},
			want:    Rect{X: 0, Y: 33, Width: 1512, Height: 949},
			floor:   Size{960, 640},
		},
		{
			name:    "saved frame partly off-screen is pulled in",
			saved:   saved(1000, 700, 1000, 700),
			screens: []Screen{laptop},
			want:    Rect{X: 512, Y: 282, Width: 1000, Height: 700},
			floor:   Size{960, 640},
		},
		{
			name:    "saved frame below the minimum grows to it",
			saved:   saved(100, 100, 500, 300),
			screens: []Screen{laptop},
			want:    Rect{X: 100, Y: 100, Width: 960, Height: 640},
			floor:   Size{960, 640},
		},
		{
			name:    "straddling frame goes to the screen it overlaps most",
			saved:   saved(1400, 100, 1300, 800),
			screens: []Screen{laptop, monitor},
			want:    Rect{X: 1512, Y: 100, Width: 1300, Height: 800},
			floor:   Size{960, 640},
		},
		{
			name:    "monitor removed: centred on the primary",
			saved:   saved(2000, 100, 1200, 800),
			screens: []Screen{laptop},
			want:    Rect{X: (1512 - 1200) / 2, Y: 33 + (949-800)/2, Width: 1200, Height: 800},
			floor:   Size{960, 640},
		},
		{
			name:    "large frame on a secondary screen is pulled into its work area",
			saved:   saved(2000, 100, 2400, 1300),
			screens: []Screen{monitor, laptop},
			want:    Rect{X: 4072 - 2400, Y: -200 + 1415 - 1300, Width: 2400, Height: 1300},
			floor:   Size{960, 640},
		},
		{
			name:    "monitor removed, frame larger than what is left",
			saved:   saved(5000, 100, 2400, 1300),
			screens: []Screen{laptop},
			want:    Rect{X: 0, Y: 33, Width: 1512, Height: 949},
			floor:   Size{960, 640},
		},
		{
			name:    "an empty saved frame is no frame",
			saved:   saved(10, 10, 0, 0),
			screens: []Screen{laptop},
			want:    Rect{X: 56, Y: 57, Width: 1400, Height: 900},
			floor:   Size{960, 640},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, floor, ok := Place(tc.saved, tc.screens)
			if !ok {
				t.Fatal("not placed")
			}
			if got != tc.want || floor != tc.floor {
				t.Errorf("Place = %+v floor %+v, want %+v floor %+v", got, floor, tc.want, tc.floor)
			}
		})
	}

	if _, _, ok := Place(nil, nil); ok {
		t.Error("no screens: want ok=false")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", FileName)
	if r, err := Load(path); r != nil || err != nil {
		t.Fatalf("missing file = %v, %v; want nil, nil", r, err)
	}
	want := Rect{X: -1200, Y: 40, Width: 1400, Height: 900}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got == nil || *got != want {
		t.Errorf("Load = %v, %v; want %v", got, err, want)
	}
}
