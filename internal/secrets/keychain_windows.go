//go:build windows

package secrets

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const osSecretService = "metiq"

type osBackend struct{}

func NewOSBackend() SecretBackend { return osBackend{} }

func (osBackend) Name() string          { return "windows-credential-manager" }
func (osBackend) ProtectedAtRest() bool { return true }

func (osBackend) Get(key string) (string, bool, error) {
	target := osSecretService + ":" + strings.TrimSpace(key)
	cred, err := windowsCredRead(target)
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok && errno == windowsErrNotFound {
			return "", false, nil
		}
		return "", false, fmt.Errorf("credential manager read: %w", err)
	}
	return cred, true, nil
}

func (osBackend) Set(key, value string) error {
	target := osSecretService + ":" + key
	if err := exec.Command("cmdkey", "/generic:"+target, "/user:"+key, "/pass:"+value).Run(); err != nil {
		return fmt.Errorf("cmdkey store: %w", err)
	}
	return nil
}

func (osBackend) Delete(key string) error {
	target := osSecretService + ":" + strings.TrimSpace(key)
	_ = exec.Command("cmdkey", "/delete:"+target).Run()
	return nil
}

const (
	credTypeGeneric                  = 1
	windowsErrNotFound syscall.Errno = 1168
)

type credentialW struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32     = syscall.NewLazyDLL("advapi32.dll")
	procCredRead = advapi32.NewProc("CredReadW")
	procCredFree = advapi32.NewProc("CredFree")
)

func windowsCredRead(target string) (string, error) {
	targetPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return "", err
	}
	var credPtr *credentialW
	r1, _, callErr := procCredRead.Call(uintptr(unsafe.Pointer(targetPtr)), uintptr(credTypeGeneric), 0, uintptr(unsafe.Pointer(&credPtr)))
	if r1 == 0 {
		if callErr != syscall.Errno(0) {
			return "", callErr
		}
		return "", syscall.EINVAL
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credPtr)))
	if credPtr == nil || credPtr.CredentialBlob == nil || credPtr.CredentialBlobSize == 0 {
		return "", nil
	}
	blob := unsafe.Slice(credPtr.CredentialBlob, credPtr.CredentialBlobSize)
	return decodeCredentialBlob(blob), nil
}

func decodeCredentialBlob(blob []byte) string {
	if len(blob)%2 == 0 && looksUTF16LE(blob) {
		words := make([]uint16, len(blob)/2)
		for i := range words {
			words[i] = uint16(blob[2*i]) | uint16(blob[2*i+1])<<8
		}
		return strings.TrimRight(string(utf16.Decode(words)), "\x00")
	}
	return strings.TrimRight(string(blob), "\x00")
}

func looksUTF16LE(blob []byte) bool {
	if len(blob) < 2 {
		return false
	}
	zeros := 0
	for i := 1; i < len(blob); i += 2 {
		if blob[i] == 0 {
			zeros++
		}
	}
	return zeros >= len(blob)/4
}
