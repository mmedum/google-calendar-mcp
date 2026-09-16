//go:build windows

package credentials

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// restrictToOwner replaces the file's access list with one that grants
// the current user everything and nobody else anything.
//
// This is what 0600 was supposed to do and does not. Go's file modes do
// not map to Windows ACLs: `os.WriteFile(path, data, 0o600)` only
// decides whether the read-only attribute is set, so the file keeps
// whatever the parent directory's inheritance gave it — the first CI run
// on Windows reported the token file as 0666 (§18 row 47).
//
// PROTECTED_DACL_SECURITY_INFORMATION is the half that matters as much
// as the grant: without it the inherited entries stay, and the new
// entry is added alongside whatever already had access rather than
// replacing it.
//
// Only the current user is granted. Administrators and SYSTEM can reach
// the file regardless, by taking ownership, so naming them would widen
// the list without adding anything they do not already have.
func restrictToOwner(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("credentials: reading this account's SID: %w", err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("credentials: building the access list: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		return fmt.Errorf("credentials: restricting %s to this account: %w", path, err)
	}
	return nil
}

func fileProtection() string {
	return "restricted to your Windows account by an explicit ACL"
}
