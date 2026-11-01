package services

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/tonyflux/pkp/internal/update"
)

const updateReceiptName = "install-result.json"

func updateInstallError(code string) error {
	switch code {
	case "integrity":
		return update.ErrIntegrity
	case "installLocation":
		return update.ErrInstallLocation
	case "installPermission":
		return update.ErrInstallPermission
	case "installBusy":
		return update.ErrInstallBusy
	case "installRollback":
		return update.ErrInstallRollback
	default:
		return errors.New("update: installation failed; see install.log")
	}
}

// A receipt is UI feedback, never authority to install or trust a package.
func consumeUpdateResult(cacheDir, current string) (phase, code string) {
	if cacheDir == "" {
		return "", ""
	}
	path := filepath.Join(cacheDir, updateReceiptName)
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()
	defer os.Remove(path)
	var receipt struct {
		Version string `json:"version"`
		Error   string `json:"error"`
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &receipt) != nil {
		return "", ""
	}
	if _, err := update.Version(receipt.Version); err != nil {
		return "", ""
	}
	if receipt.Error != "" {
		return "error", updateError(updateInstallError(receipt.Error), "install")
	}
	if receipt.Version == current {
		return "updated", ""
	}
	return "", ""
}
