package userconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v2/internal/userconfig"
)

// isolate points the package at a temporary directory. Every test in
// this package uses it: a test that writes to the real config directory
// deletes a maintainer's credentials, which is how a sibling lost its
// refresh token five times in one session.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(userconfig.EnvDir, dir)
	return dir
}

func TestSaveLoadRoundTrip(t *testing.T) {
	isolate(t)
	want := userconfig.Config{
		ClientSecretPath: "/somewhere/client_secret.json",
		AccountEmail:     "someone@example.test",
		TokenStore:       "keyring",
		Scopes:           []string{"a", "b"},
	}
	if err := userconfig.Save("default", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := userconfig.Load("default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ClientSecretPath != want.ClientSecretPath || got.AccountEmail != want.AccountEmail ||
		got.TokenStore != want.TokenStore || len(got.Scopes) != 2 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("Save did not stamp UpdatedAt")
	}
}

func TestLoadMissingIsErrNotFound(t *testing.T) {
	isolate(t)
	if _, err := userconfig.Load("default"); !errors.Is(err, userconfig.ErrNotFound) {
		t.Fatalf("Load on a fresh profile = %v, want ErrNotFound", err)
	}
}

func TestProfileDirsAreSeparate(t *testing.T) {
	base := isolate(t)
	def, err := userconfig.ProfileDir("default")
	if err != nil {
		t.Fatal(err)
	}
	work, err := userconfig.ProfileDir("work")
	if err != nil {
		t.Fatal(err)
	}
	if def != base {
		t.Fatalf("default profile dir = %q, want the base %q", def, base)
	}
	if work != filepath.Join(base, "profiles", "work") {
		t.Fatalf("work profile dir = %q", work)
	}
	if def == work {
		t.Fatal("two profiles share a directory")
	}
}

func TestSaveWritesOwnerOnly(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("file modes differ on Windows")
	}
	isolate(t)
	if err := userconfig.Save("default", userconfig.Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p, err := userconfig.Path("default")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// A mode means nothing here. internal/fileperm restricts the
		// file with an access list instead, and its own test reads that
		// list back (§18 row 47).
		return
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("config file mode %o is readable by others", mode)
	}
}

func TestResolveClientSecretPathOrder(t *testing.T) {
	isolate(t)
	// Nothing stored: the default location.
	def, err := userconfig.DefaultClientSecretPath("default")
	if err != nil {
		t.Fatal(err)
	}
	got, err := userconfig.ResolveClientSecretPath("default")
	if err != nil {
		t.Fatal(err)
	}
	if got != def {
		t.Fatalf("with nothing stored, got %q, want the default %q", got, def)
	}

	// Stored beats the default.
	if err := userconfig.Save("default", userconfig.Config{ClientSecretPath: "/stored.json"}); err != nil {
		t.Fatal(err)
	}
	if got, _ = userconfig.ResolveClientSecretPath("default"); got != "/stored.json" {
		t.Fatalf("stored path ignored: %q", got)
	}

	// An override beats both, and a blank override is not an override.
	if got, _ = userconfig.ResolveClientSecretPath("default", "/override.json"); got != "/override.json" {
		t.Fatalf("override ignored: %q", got)
	}
	if got, _ = userconfig.ResolveClientSecretPath("default", "   "); got != "/stored.json" {
		t.Fatalf("a whitespace override was treated as a path: %q", got)
	}
}

func TestProfilesAndSharingClient(t *testing.T) {
	isolate(t)
	if err := userconfig.Save("default", userconfig.Config{ClientSecretPath: "/shared.json"}); err != nil {
		t.Fatal(err)
	}
	if err := userconfig.Save("work", userconfig.Config{ClientSecretPath: "/shared.json"}); err != nil {
		t.Fatal(err)
	}
	if err := userconfig.Save("other", userconfig.Config{ClientSecretPath: "/different.json"}); err != nil {
		t.Fatal(err)
	}

	names, err := userconfig.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("Profiles() = %v, want three", names)
	}

	shared, err := userconfig.SharingClient("default")
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 1 || shared[0] != "work" {
		t.Fatalf("SharingClient(default) = %v, want [work]", shared)
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	isolate(t)
	if err := userconfig.Save("default", userconfig.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := userconfig.Remove("default"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := userconfig.Remove("default"); err != nil {
		t.Fatalf("Remove on a missing file must succeed: %v", err)
	}
}

func TestBaseDirHonorsTheEnvironmentOverride(t *testing.T) {
	dir := isolate(t)
	got, err := userconfig.BaseDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("BaseDir() = %q, want the override %q", got, dir)
	}
}

func TestTokenAndClientSecretPathsLiveInTheProfile(t *testing.T) {
	base := isolate(t)
	for _, c := range []struct {
		name    string
		get     func(string) (string, error)
		wantEnd string
	}{
		{"token", userconfig.TokenFilePath, "token.json"},
		{"client secret", userconfig.DefaultClientSecretPath, "client_secret.json"},
		{"config", userconfig.Path, "config.json"},
	} {
		t.Run(c.name, func(t *testing.T) {
			def, err := c.get("default")
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(def) != base {
				t.Fatalf("the default profile's %s is at %q, outside the base %q", c.name, def, base)
			}
			if filepath.Base(def) != c.wantEnd {
				t.Fatalf("%s is named %q", c.name, filepath.Base(def))
			}

			work, err := c.get("work")
			if err != nil {
				t.Fatal(err)
			}
			if work == def {
				t.Fatalf("two profiles share a %s path", c.name)
			}
		})
	}
}

func TestSharingClientWithNothingStored(t *testing.T) {
	isolate(t)
	if _, err := userconfig.SharingClient("default"); !errors.Is(err, userconfig.ErrNotFound) {
		t.Fatalf("SharingClient on an unconfigured profile = %v, want ErrNotFound", err)
	}

	// A profile with no client path shares with nobody.
	if err := userconfig.Save("default", userconfig.Config{}); err != nil {
		t.Fatal(err)
	}
	got, err := userconfig.SharingClient("default")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("SharingClient = %v, want none", got)
	}
}

func TestProfilesOnAnEmptyDirectory(t *testing.T) {
	isolate(t)
	got, err := userconfig.Profiles()
	if err != nil {
		t.Fatalf("Profiles on an empty config dir: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Profiles = %v, want none", got)
	}
}

func TestLoadRejectsCorruptJSON(t *testing.T) {
	isolate(t)
	p, err := userconfig.Path("default")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := userconfig.Load("default"); err == nil {
		t.Fatal("a corrupt config file loaded successfully")
	}
}

func TestResolveClientSecretPathSurfacesALoadFailure(t *testing.T) {
	isolate(t)
	p, err := userconfig.Path("default")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A corrupt profile must not be silently treated as an empty one:
	// that would resolve to the default path and read the wrong client.
	if _, err := userconfig.ResolveClientSecretPath("default"); err == nil {
		t.Fatal("a corrupt profile resolved to a client secret path anyway")
	}
	// Unless an override was given, which needs no profile at all.
	got, err := userconfig.ResolveClientSecretPath("default", "/explicit.json")
	if err != nil || got != "/explicit.json" {
		t.Fatalf("an explicit override should not need the profile: %q %v", got, err)
	}
}

func TestProfilesSkipsNonDirectories(t *testing.T) {
	base := isolate(t)
	if err := userconfig.Save("default", userconfig.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A stray file, and a directory with no config in it. Neither is a
	// profile.
	if err := os.WriteFile(filepath.Join(base, "profiles", "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "profiles", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := userconfig.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "default" {
		t.Fatalf("Profiles = %v, want just [default]", got)
	}
}

func TestSharingClientIgnoresUnreadableProfiles(t *testing.T) {
	base := isolate(t)
	if err := userconfig.Save("default", userconfig.Config{ClientSecretPath: "/shared.json"}); err != nil {
		t.Fatal(err)
	}
	if err := userconfig.Save("broken", userconfig.Config{ClientSecretPath: "/shared.json"}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the second one.
	if err := os.WriteFile(filepath.Join(base, "profiles", "broken", "config.json"),
		[]byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := userconfig.SharingClient("default")
	if err != nil {
		t.Fatalf("one unreadable profile broke the whole check: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("SharingClient = %v; an unreadable profile cannot be claimed to share a client", got)
	}
}
