//go:build linux

package lanshare

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// A persistent token hashes content once; inotify invalidates it during streaming
// without hashing an entire video before and after every network buffer.
type localReadProof struct {
	fd int
	invalid bool
}

func newLocalReadProof(f *os.File, token string) (*localReadProof, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC|unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	proof := &localReadProof{fd: fd}
	_, err = unix.InotifyAddWatch(fd, "/proc/self/fd/"+strconv.FormatUint(uint64(f.Fd()), 10), unix.IN_MODIFY|unix.IN_ATTRIB|unix.IN_MOVE_SELF|unix.IN_DELETE_SELF|unix.IN_UNMOUNT)
	if err != nil {
		proof.Close()
		return nil, err
	}
	_, current, _, err := handleIdentity(f)
	if err != nil || current != token {
		proof.Close()
		return nil, errUnavailable
	}
	if err = proof.Check(); err != nil {
		proof.Close()
		return nil, err
	}
	return proof, nil
}

func (p *localReadProof) Check() error {
	if p.invalid || p.fd < 0 {
		return errUnavailable
	}
	var events [4096]byte
	for {
		_, err := unix.Read(p.fd, events[:])
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EAGAIN) {
			return nil
		}
		// Every subscribed event (including IN_IGNORED/IN_Q_OVERFLOW) revokes
		// the proof. Never accept a partly observed event stream.
		p.invalid = true
		return errUnavailable
	}
}

func (p *localReadProof) Close() {
	if p.fd >= 0 {
		unix.Close(p.fd)
		p.fd = -1
	}
}
