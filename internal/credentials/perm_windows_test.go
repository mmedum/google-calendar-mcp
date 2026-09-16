//go:build windows

package credentials_test

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestFileIsRestrictedToOwner is the Windows half of the guarantee the
// warnings make, and it reads the access list back rather than trusting
// the call that set it.
//
// 0600 is a no-op here: the first CI run on Windows found the token file
// at 0666, readable by every account on the machine, while both warnings
// said "mode 0600" (§18 row 47). What replaced it has to be checked the
// way Windows describes access, not the way Unix does.
func TestFileIsRestrictedToOwner(t *testing.T) {
	kr := newFake()
	kr.failSet = errors.New("unavailable")
	s, _ := store(t, kr, false)
	if _, err := s.Save("refresh-abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.FilePath); err != nil {
		t.Fatal(err)
	}

	sd, err := windows.GetNamedSecurityInfo(s.FilePath, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the file's security descriptor: %v", err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("reading the descriptor's control bits: %v", err)
	}
	// Without SE_DACL_PROTECTED the entries inherited from the parent
	// directory are still in force, and the grant added below them
	// widens access rather than replacing it.
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("the access list still inherits from its parent, so it does not restrict anything")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("reading the access list: %v", err)
	}
	if dacl == nil {
		t.Fatal("no access list on the token file, which means everyone has access")
	}
	// One entry: this account. Anything more is somebody else.
	if dacl.AceCount != 1 {
		t.Fatalf("the token file's access list has %d entries; only this account should be on it",
			dacl.AceCount)
	}
}
