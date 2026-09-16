package main

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/mmedum/google-calendar-mcp/internal/credentials"
)

// TestMain makes it STRUCTURALLY impossible for a test in this package
// to reach the real OS keyring.
//
// This is not caution, it is a repair. Without it, every test that did
// not install its own fake ran against the maintainer's real keyring
// under the default profile — and `logout` revokes the grant at Google
// and deletes the token. Running `go test ./cmd/...` signed the
// maintainer out of their own account.
//
// The sibling servers hit exactly this and the warning is in their
// source: a test can redirect the config directory and the environment,
// and it CANNOT redirect the OS keyring, so isolation that stops at the
// filesystem is not isolation. That warning is quoted in
// internal/credentials and was written into this repository before this
// happened, which is the point the standard's preamble makes: a rule
// stated is not a rule kept.
//
// Installed for the whole package rather than per test, because a rule
// each test has to remember is a rule the next test forgets.
func TestMain(m *testing.M) {
	keyringBackend = &packageKeyring{items: map[string]string{}}
	os.Exit(m.Run())
}

// packageKeyring is the only keyring any test in this package can see.
type packageKeyring struct{ items map[string]string }

func (k *packageKeyring) Get(service, account string) (string, error) {
	v, ok := k.items[service+"/"+account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (k *packageKeyring) Set(service, account, secret string) error {
	k.items[service+"/"+account] = secret
	return nil
}

func (k *packageKeyring) Delete(service, account string) error {
	delete(k.items, service+"/"+account)
	return nil
}

// TestTheRealKeyringIsUnreachableFromTests is the decoy.
//
// It asserts the substitution actually happened, so a refactor that
// drops TestMain fails here rather than silently going back to deleting
// the maintainer's credentials.
func TestTheRealKeyringIsUnreachableFromTests(t *testing.T) {
	if _, ok := keyringBackend.(*packageKeyring); !ok {
		t.Fatalf("keyringBackend is %T, not the package fake; a test in this package can "+
			"revoke and delete the maintainer's real refresh token", keyringBackend)
	}
	// And the production constructor still returns the real one, so the
	// substitution is a test-time choice rather than a permanent change.
	if _, ok := credentials.OSKeyring().(*packageKeyring); ok {
		t.Fatal("credentials.OSKeyring() returns the test fake; production would have no keyring at all")
	}
}
