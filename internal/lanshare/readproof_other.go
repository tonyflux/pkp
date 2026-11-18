//go:build !linux

package lanshare

import "os"

type localReadProof struct {
	file *os.File
	token string
}

func newLocalReadProof(f *os.File, token string) (*localReadProof, error) {
	proof := &localReadProof{file: f, token: token}
	if err := proof.Check(); err != nil {
		return nil, err
	}
	return proof, nil
}

func (p *localReadProof) Check() error {
	_, current, _, err := handleIdentity(p.file)
	if err != nil || current != p.token {
		return errUnavailable
	}
	return nil
}

func (p *localReadProof) Close() {}
