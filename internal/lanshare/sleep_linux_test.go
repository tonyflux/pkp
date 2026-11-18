//go:build linux

package lanshare

import (
	"os"
	"sync"
	"testing"
)

func TestLinuxSleepBlockCloseReleasesDescriptorOnce(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	block := &linuxSleepBlock{file: write}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if err := block.Close(); err != nil {
				t.Errorf("Close: %s", err)
			}
		})
	}
	group.Wait()
	if _, err := write.Write([]byte("x")); err == nil {
		t.Fatal("inhibitor descriptor remained open")
	}
}
