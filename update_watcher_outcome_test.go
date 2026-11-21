package main

import "testing"

func TestWindowsUpdateOutcome(t *testing.T) {
	for _, tc := range []struct {
		exit    uint32
		code    string
		restart bool
	}{
		{0, "", true},
		{2, "install", true},
		{3, "install", true},
		{4, "install", true},
		{65, "install", true},
		{71, "installBusy", true},
		{72, "installRollback", false},
		{73, "installLocation", true},
	} {
		code, restart := windowsUpdateOutcome(tc.exit)
		if code != tc.code || restart != tc.restart {
			t.Errorf("exit %d: got %q, restart %v; want %q, restart %v", tc.exit, code, restart, tc.code, tc.restart)
		}
	}
}
