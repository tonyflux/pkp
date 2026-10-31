package services

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
)

func TestTierOf(t *testing.T) {
	for _, c := range []struct {
		w, h, tier int
	}{
		{3840, 2160, 2160},
		{3840, 1600, 2160}, // cinema crop of 4K
		{2560, 1440, 1440},
		{1920, 1080, 1080},
		{1920, 800, 1080},  // cinema crop of 1080p
		{1440, 1080, 1080}, // 4:3
		{1080, 1440, 1080}, // portrait original the server calls "720P"
		{960, 1280, 720},   // portrait transcode the server calls "480P"
		{1280, 720, 720},
		{1280, 536, 720},
		{852, 480, 480},
		{640, 852, 480}, // portrait transcode, also "480P" to the server
		{640, 360, 360},
		{0, 0, 0}, // no picture metadata
	} {
		if got := tierOf(c.w, c.h); got != c.tier {
			t.Errorf("tierOf(%d, %d) = %d, want %d", c.w, c.h, got, c.tier)
		}
	}
}

// A get with usage=PLAY as the production API answers it: the original plus
// HEVC transcodes. Some answers carry two transcodes of one tier, some a
// transcode the account may not use.
func TestRenditionsOf(t *testing.T) {
	var f drive.File
	err := json.Unmarshal([]byte(`{"kind":"drive#file","id":"f","phase":"PHASE_TYPE_COMPLETE",
		"web_content_link":"https://dl/content",
		"medias":[
		 {"media_name":"Original","is_origin":true,"is_visible":true,"resolution_name":"1080P",
		  "video":{"width":1920,"height":1080,"bit_rate":5525144},"link":{"url":"https://dl/origin"}},
		 {"media_name":"1080P","is_default":true,"is_visible":true,
		  "video":{"width":1920,"height":1080,"bit_rate":951667},"link":{"url":"https://dl/1080"}},
		 {"media_name":"480P","is_visible":true,"video":{"width":852,"height":480,"bit_rate":400000},"link":{"url":"https://dl/480-thin"}},
		 {"media_name":"480P","is_visible":true,"video":{"width":852,"height":480,"bit_rate":508347},"link":{"url":"https://dl/480"}},
		 {"media_name":"720P","is_visible":true,"video":{"width":1280,"height":720,"bit_rate":626972},"link":{"url":"https://dl/720"}},
		 {"media_name":"360P","is_visible":true,"need_more_quota":true,"video":{"width":640,"height":360},"link":{"url":"https://dl/360"}},
		 {"media_name":"240P","is_visible":false,"video":{"width":426,"height":240},"link":{"url":"https://dl/240"}},
		 {"media_name":"odd","is_visible":true,"link":{"url":"https://dl/odd"}}
		]}`), &f)
	if err != nil {
		t.Fatal(err)
	}
	got := renditionsOf(f)
	var keys, urls []string
	for _, r := range got {
		keys = append(keys, r.Key)
		urls = append(urls, r.url)
	}
	if want := []string{OriginKey, "1080", "720", "480"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	if want := []string{"https://dl/origin", "https://dl/1080", "https://dl/720", "https://dl/480"}; !slices.Equal(urls, want) {
		t.Errorf("urls = %v, want %v (the richer 480 of the two)", urls, want)
	}
	if o := got[0]; !o.Origin || o.Tier != 1080 || o.BitRate != 5525144 {
		t.Errorf("origin = %+v", o.Quality)
	}
}

// When the original media needs more quota the download link still plays the
// same bytes, and a file the server never transcoded has the original only.
func TestRenditionsOfOriginFallsBackToContentLink(t *testing.T) {
	f := drive.File{
		WebContentLink: "https://dl/content",
		Medias:         []drive.Media{{IsOrigin: true, NeedMoreQuota: true, Link: drive.Link{URL: "https://dl/origin"}}},
	}
	got := renditionsOf(f)
	if len(got) != 1 || got[0].Key != OriginKey || got[0].url != "https://dl/content" {
		t.Fatalf("renditions = %+v, want the original through the content link", got)
	}
	if got := renditionsOf(drive.File{}); len(got) != 0 {
		t.Errorf("a file without links has renditions %+v", got)
	}
}

func TestPick(t *testing.T) {
	full := []Quality{
		{Key: OriginKey, Origin: true, Tier: 1080},
		{Key: "1080", Tier: 1080},
		{Key: "720", Tier: 720},
		{Key: "480", Tier: 480},
	}
	fourK := []Quality{{Key: OriginKey, Origin: true, Tier: 2160}, {Key: "1080", Tier: 1080}, {Key: "480", Tier: 480}}
	small := []Quality{{Key: OriginKey, Origin: true, Tier: 720}, {Key: "480", Tier: 480}}
	unknown := []Quality{{Key: OriginKey, Origin: true}}
	for _, c := range []struct {
		name    string
		options []Quality
		choice  string
		want    string
	}{
		{"auto plays the original", full, AutoQuality, OriginKey},
		{"origin", full, OriginKey, OriginKey},
		{"a tier prefers its transcode over an original of the same tier", full, "1080", "1080"},
		{"exact tier", full, "720", "720"},
		{"a missing tier takes the richest under it", fourK, "720", "480"},
		{"a 1440 preference on a 4K video", fourK, "1440", "1080"},
		{"an original that fits under the tier beats a smaller transcode", small, "1080", OriginKey},
		{"nothing under the tier: the smallest", small, "360", "480"},
		{"an original without metadata is all there is", unknown, "720", OriginKey},
		{"no options", nil, AutoQuality, ""},
	} {
		if got := pick(c.options, c.choice); got != c.want {
			t.Errorf("%s: pick(%q) = %q, want %q", c.name, c.choice, got, c.want)
		}
	}
}

func TestLower(t *testing.T) {
	options := []Quality{
		{Key: OriginKey, Origin: true, BitRate: 5_500_000},
		{Key: "1080", BitRate: 950_000},
		{Key: "720", BitRate: 620_000},
		{Key: "480", BitRate: 500_000},
	}
	for current, want := range map[string]string{OriginKey: "1080", "1080": "720", "720": "480", "480": "", "gone": ""} {
		if got := lower(options, current); got != want {
			t.Errorf("lower(%q) = %q, want %q", current, got, want)
		}
	}
	// A transcode heavier than a lean original is no step down.
	lean := []Quality{{Key: OriginKey, Origin: true, BitRate: 800_000}, {Key: "1080", BitRate: 1_700_000}, {Key: "720", BitRate: 700_000}}
	if got := lower(lean, OriginKey); got != "720" {
		t.Errorf("lower from a lean original = %q, want 720", got)
	}
	// Without metadata the original counts as the heaviest.
	if got := lower([]Quality{{Key: OriginKey, Origin: true}, {Key: "1080", BitRate: 1_000_000}}, OriginKey); got != "1080" {
		t.Errorf("lower from an unmeasured original = %q, want 1080", got)
	}
}
