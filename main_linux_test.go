//go:build linux

package main

import (
	"regexp"
	"testing"
)

func TestProfileInstanceNamesAreValidDBusNames(t *testing.T) {
	part := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	seen := map[string]bool{}
	for _, dir := range []string{"", "/tmp/profile-a", "/tmp/profile-b", "/tmp/含空格 profile"} {
		id := instanceID(dir)
		if id != instanceID(dir) || seen[id] {
			t.Fatalf("profile identity is unstable or collides: %q", id)
		}
		seen[id] = true
		start := 0
		for i := 0; i <= len(id); i++ {
			if i == len(id) || id[i] == '.' {
				if !part.MatchString(id[start:i]) {
					t.Fatalf("invalid D-Bus element in %q", id)
				}
				start = i + 1
			}
		}
	}
}
