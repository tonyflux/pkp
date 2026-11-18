//go:build linux

package services

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestLinuxFileURIPreservesSpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "照片 #1? 100%.mp4")
	uri, err := linuxFileURI(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" || parsed.Path != path || parsed.Fragment != "" || parsed.RawQuery != "" {
		t.Fatalf("file URI %q did not preserve local path: %v", uri, err)
	}
	if _, err := linuxFileURI(""); err == nil {
		t.Fatal("empty path became the working directory")
	}
}

func TestLinuxPortalResponseRequiresActualSuccess(t *testing.T) {
	results := map[string]dbus.Variant{}
	for _, tc := range []struct {
		name string
		body []any
		ok   bool
	}{
		{"success", []any{uint32(0), results}, true},
		{"cancelled", []any{uint32(1), results}, false},
		{"failed", []any{uint32(2), results}, false},
		{"unknown", []any{uint32(3), results}, false},
		{"wrong results type", []any{uint32(0), "invalid"}, false},
		{"wrong code type", []any{int32(0), results}, false},
		{"missing results", []any{uint32(0)}, false},
		{"empty", nil, false},
	} {
		if err := linuxPortalResponse(tc.body); (err == nil) != tc.ok {
			t.Errorf("%s: response error = %v", tc.name, err)
		}
	}
}

func TestGNOMEDesktopRequiresAnActualDesktopName(t *testing.T) {
	for _, desktop := range []string{"GNOME", "ubuntu:GNOME", "Unity"} {
		if !isGNOMEDesktop(desktop) {
			t.Errorf("GNOME activation unavailable for %q", desktop)
		}
	}
	for _, desktop := range []string{"KDE", "sway", "not-gnome", ""} {
		if isGNOMEDesktop(desktop) {
			t.Errorf("unrelated desktop %q selected GNOME activation", desktop)
		}
	}
}
