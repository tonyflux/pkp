//go:build darwin

package services

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tonyflux/pkp/internal/i18n"
)

const (
	// keychainService names PKP's items in the login keychain; it is the
	// app's bundle identifier.
	keychainService = "com.pkp.desktop"
	// keychainPrefix marks the base64 form a password is stored in, which
	// keeps quotes and non-ASCII intact through the security tool's parser
	// and its output.
	keychainPrefix = "pkp-base64:"
	securityTool   = "/usr/bin/security"
	// itemNotFound is the security tool's exit status for a missing item.
	itemNotFound = 44
)

// keychainName is what the page and errors call the vault, in the language
// in force.
func keychainName() string { return i18n.T("钥匙串") }

// keychainVault keeps passwords in the login keychain through the security
// tool, without cgo. A password is handed over on the tool's stdin (its
// interactive mode), never on a command line another process could read.
type keychainVault struct{}

func newSystemVault() PasswordVault { return keychainVault{} }

func (keychainVault) Password(account string) (string, error) {
	out, err := exec.Command(securityTool, "find-generic-password", "-s", keychainService, "-a", account, "-w").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == itemNotFound {
			return "", ErrNoSavedPassword
		}
		return "", fmt.Errorf("keychain: read %s: %w", account, err)
	}
	encoded, ok := strings.CutPrefix(strings.TrimSpace(string(out)), keychainPrefix)
	if !ok {
		return "", fmt.Errorf("keychain: the item for %s is not one PKP wrote", account)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("keychain: decode %s: %w", account, err)
	}
	return string(raw), nil
}

func (k keychainVault) SetPassword(account, password string) error {
	if strings.ContainsAny(account, "\r\n") {
		return fmt.Errorf("keychain: account %q cannot be stored", account)
	}
	command := fmt.Sprintf("add-generic-password -U -s %s -a %s -l PKP -w %s\n",
		quoteArg(keychainService), quoteArg(account), quoteArg(keychainPrefix+base64.StdEncoding.EncodeToString([]byte(password))))
	cmd := exec.Command(securityTool, "-i")
	cmd.Stdin = strings.NewReader(command)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain: store %s: %w: %s", account, err, strings.TrimSpace(string(out)))
	}
	// Read back, so a store that did not take is an error now rather than a
	// password missing at the next sign-in.
	if got, err := k.Password(account); err != nil || got != password {
		return fmt.Errorf("keychain: store %s: the item did not read back", account)
	}
	return nil
}

func (keychainVault) DeletePassword(account string) error {
	err := exec.Command(securityTool, "delete-generic-password", "-s", keychainService, "-a", account).Run()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == itemNotFound) {
		return fmt.Errorf("keychain: delete %s: %w", account, err)
	}
	return nil
}

// quoteArg double-quotes s for the security tool's interactive mode, which
// splits its command line on spaces and reads backslash escapes inside
// double quotes. It does not join adjacent quoted pieces the way a shell does.
func quoteArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
