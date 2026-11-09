//go:build windows

package knobs

import (
	"errors"
	"fmt"
	"io/fs"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownerOnly reports, as an error, a secrets file its owner alone cannot claim. Windows file modes carry no access
// rights, so the file's security descriptor decides: its owner is the current user, SYSTEM or Administrators, and
// every access-allowed entry of its DACL grants one of those three; a missing (null) DACL grants everyone access.
func ownerOnly(path string, _ fs.FileInfo) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%s: reading its security descriptor: %w", path, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("reading the current user: %w", err)
	}
	trusted := func(sid *windows.SID) bool {
		return sid.Equals(user.User.Sid) || sid.IsWellKnown(windows.WinLocalSystemSid) ||
			sid.IsWellKnown(windows.WinBuiltinAdministratorsSid)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("%s: reading its owner: %w", path, err)
	}
	if !trusted(owner) {
		return fmt.Errorf("%s is owned by %s; the current user, SYSTEM or Administrators must own it", path, owner)
	}
	return checkDACL(path, sd, trusted)
}

// checkDACL fails when the DACL is missing or one of its access-allowed entries grants a SID that is not trusted.
func checkDACL(path string, sd *windows.SECURITY_DESCRIPTOR, trusted func(*windows.SID) bool) error {
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errors.Join(fmt.Errorf("%s has no DACL: everyone may have access", path), err)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("%s: reading its DACL: %w", path, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !trusted(sid) {
			return fmt.Errorf("%s grants access to %s; the owner alone may have access", path, sid)
		}
	}
	return nil
}
