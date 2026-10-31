//go:build windows

package services

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tonyflux/pkp/internal/i18n"
)

const (
	// credentialPrefix starts the target name of PKP's items in the Windows
	// Credential Manager (Credential Manager › Windows Credentials): the app's bundle
	// identifier, like the service of the macOS keychain items, then the
	// account.
	credentialPrefix = "com.pkp.desktop/"
	credTypeGeneric  = 1 // CRED_TYPE_GENERIC
	// credPersistLocalMachine keeps the item across sign-outs, on this
	// computer only: a roaming profile does not carry it elsewhere.
	credPersistLocalMachine = 2
	// credMaxBlobSize is CRED_MAX_CREDENTIAL_BLOB_SIZE, in bytes.
	credMaxBlobSize = 5 * 512
)

// keychainName is what the page and errors call the vault, in the language
// in force: Credential Manager is the name Windows gives it.
func keychainName() string { return i18n.T("凭据管理器") }

var (
	modAdvapi32     = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW   = modAdvapi32.NewProc("CredReadW")
	procCredWriteW  = modAdvapi32.NewProc("CredWriteW")
	procCredDeleteW = modAdvapi32.NewProc("CredDeleteW")
	procCredFree    = modAdvapi32.NewProc("CredFree")
)

// winCredential is CREDENTIALW.
type winCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// credManagerVault keeps passwords as generic credentials of the Windows
// user signed in, through advapi32 without cgo. The blob is the password's
// UTF-8 bytes.
type credManagerVault struct{}

func newSystemVault() PasswordVault { return credManagerVault{} }

func (credManagerVault) Password(account string) (string, error) {
	target, err := credentialTarget(account)
	if err != nil {
		return "", err
	}
	var c *winCredential
	if r, _, e := procCredReadW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&c))); r == 0 {
		if errors.Is(e, windows.ERROR_NOT_FOUND) {
			return "", ErrNoSavedPassword
		}
		return "", fmt.Errorf("credential manager: read %s: %w", account, e)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(c)))
	return string(unsafe.Slice(c.CredentialBlob, c.CredentialBlobSize)), nil
}

func (credManagerVault) SetPassword(account, password string) error {
	if len(password) > credMaxBlobSize {
		return fmt.Errorf("credential manager: the password for %s is longer than %d bytes", account, credMaxBlobSize)
	}
	target, err := credentialTarget(account)
	if err != nil {
		return err
	}
	user, err := windows.UTF16PtrFromString(account)
	if err != nil {
		return fmt.Errorf("credential manager: account %q cannot be stored", account)
	}
	comment, _ := windows.UTF16PtrFromString("PKP")
	blob := []byte(password)
	c := winCredential{
		Type:               credTypeGeneric,
		TargetName:         target,
		Comment:            comment,
		CredentialBlobSize: uint32(len(blob)),
		Persist:            credPersistLocalMachine,
		UserName:           user,
	}
	if len(blob) > 0 {
		c.CredentialBlob = &blob[0]
	}
	if r, _, e := procCredWriteW.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return fmt.Errorf("credential manager: store %s: %w", account, e)
	}
	return nil
}

func (credManagerVault) DeletePassword(account string) error {
	target, err := credentialTarget(account)
	if err != nil {
		return err
	}
	if r, _, e := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0); r == 0 && !errors.Is(e, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("credential manager: delete %s: %w", account, e)
	}
	return nil
}

func credentialTarget(account string) (*uint16, error) {
	target, err := windows.UTF16PtrFromString(credentialPrefix + account)
	if err != nil {
		return nil, fmt.Errorf("credential manager: account %q cannot be stored", account)
	}
	return target, nil
}
