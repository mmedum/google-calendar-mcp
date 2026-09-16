//go:build !windows

package credentials

import (
	"fmt"
	"os"
)

// restrictToOwner reasserts owner-only permissions after the file is in
// place. The write already asked for 0600; this catches a file an
// earlier version, a umask or a restore left wider.
func restrictToOwner(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("credentials: restricting %s to its owner: %w", path, err)
	}
	return nil
}

func fileProtection() string { return "mode 0600" }
