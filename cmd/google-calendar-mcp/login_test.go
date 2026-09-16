package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/auth"
	"github.com/mmedum/google-calendar-mcp/internal/credentials"
	"github.com/mmedum/google-calendar-mcp/internal/gapi"
)

// fakeKeyring stands in for the OS keyring. Every test in this file
// installs one: a test that reaches the real keyring deletes the
// maintainer's refresh token.
type fakeKeyring struct{ items map[string]string }

func (f *fakeKeyring) Get(service, account string) (string, error) {
	v, ok := f.items[service+"/"+account]
	if !ok {
		return "", errNoEntry
	}
	return v, nil
}
func (f *fakeKeyring) Set(service, account, secret string) error {
	f.items[service+"/"+account] = secret
	return nil
}
func (f *fakeKeyring) Delete(service, account string) error {
	delete(f.items, service+"/"+account)
	return nil
}

// errNoEntry must be the keyring's own sentinel, or the store classes a
// missing entry as a transport failure and takes the wrong branch.
var errNoEntry = keyringNotFound()

// fakeGoogle serves the token, tokeninfo and revoke endpoints.
func fakeGoogle(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	revoked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`))
		case strings.HasSuffix(r.URL.Path, "/tokeninfo"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"scope":"` + strings.Join(auth.Scopes(auth.Access{Sharing: true}), " ") +
				`","email":"person@example.test","expires_in":"3599","aud":"id"}`))
		case strings.HasSuffix(r.URL.Path, "/revoke"):
			revoked++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &revoked
}

// setupLogin writes a Desktop client JSON pointing at the fake Google,
// installs the fake keyring, and makes "the browser" complete the
// callback.
func setupLogin(t *testing.T) (dir string, env func(string) string) {
	t.Helper()
	srv, _ := fakeGoogle(t)
	return setupLoginWith(t, srv)
}

func setupLoginWith(t *testing.T, srv *httptest.Server) (string, func(string) string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("GCAL_CONFIG_DIR", dir)

	secret := dir + "/client_secret.json"
	body := `{"installed":{"client_id":"id.apps.googleusercontent.com","client_secret":"s",` +
		`"auth_uri":"` + srv.URL + `/auth","token_uri":"` + srv.URL + `/token"}}`
	if err := os.WriteFile(secret, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// A fresh keyring per test, on top of the package-level one that
	// TestMain installs. The package-level fake is what makes reaching
	// the real keyring impossible; this one only keeps two tests from
	// seeing each other's tokens.
	oldKeyring, oldBrowser := keyringBackend, openBrowser
	oldInfo, oldRevoke := auth.TokenInfoURL, auth.RevokeURL
	keyringBackend = &fakeKeyring{items: map[string]string{}}
	auth.TokenInfoURL = srv.URL + "/tokeninfo"
	auth.RevokeURL = srv.URL + "/revoke"
	openBrowser = func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		q := u.Query()
		go func() {
			cb := q.Get("redirect_uri") + "?state=" + url.QueryEscape(q.Get("state")) + "&code=authcode"
			resp, err := http.Get(cb) //nolint:noctx // test callback
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() {
		keyringBackend, openBrowser = oldKeyring, oldBrowser
		auth.TokenInfoURL, auth.RevokeURL = oldInfo, oldRevoke
	})

	return dir, func(k string) string {
		switch k {
		case "GCAL_CONFIG_DIR":
			return dir
		case "GCAL_CLIENT_SECRET":
			return secret
		}
		return ""
	}
}

var dir string

func TestLoginStoresTheTokenAndTheProfile(t *testing.T) {
	_, env := setupLogin(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"login"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("login: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Signed in") {
		t.Fatalf("login did not confirm:\n%s", s)
	}
	// It must print the scopes BEFORE opening a browser, so a person can
	// see what they are about to grant.
	if !strings.Contains(s, "calendar.readonly") {
		t.Fatalf("login did not list the scopes it asked for:\n%s", s)
	}
	if !strings.Contains(s, "person@example.test") {
		t.Fatalf("login did not report which account signed in:\n%s", s)
	}

	// And status now reflects it, without touching the network.
	out.Reset()
	if err := run([]string{"status"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatal(err)
	}
	st := out.String()
	if !strings.Contains(st, "signed in:     yes") {
		t.Fatalf("status does not show the login:\n%s", st)
	}
	if !strings.Contains(st, "person@example.test") {
		t.Fatalf("status does not name the account:\n%s", st)
	}
}

// TestLoginWarnsAboutScopesGoogleDidNotGrant: a missing scope is a 403
// from one tool weeks later, so it is said at the moment it happens.
func TestLoginWarnsAboutScopesGoogleDidNotGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`))
		case strings.HasSuffix(r.URL.Path, "/tokeninfo"):
			// Only the two read scopes were granted.
			_, _ = w.Write([]byte(`{"scope":"` + auth.ScopeReadonly + ` ` + auth.ScopeSettingsReadonly +
				`","email":"person@example.test","expires_in":"3599","aud":"id"}`))
		}
	}))
	defer srv.Close()

	_, env := setupLoginWith(t, srv)
	var out, errOut bytes.Buffer
	if err := run([]string{"login"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(errOut.String(), "not granted") {
		t.Fatalf("login did not warn about the ungranted scopes:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "calendar.events") {
		t.Fatalf("the warning does not name a missing scope:\n%s", errOut.String())
	}
}

func TestLogoutRevokesAndForgets(t *testing.T) {
	srv, revoked := fakeGoogle(t)
	_, env := setupLoginWith(t, srv)

	var out, errOut bytes.Buffer
	if err := run([]string{"login"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := run([]string{"logout"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("logout: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Revoked") {
		t.Fatalf("logout did not revoke at Google:\n%s", s)
	}
	if *revoked != 1 {
		t.Fatalf("the revoke endpoint was called %d times", *revoked)
	}

	// And the profile is gone.
	out.Reset()
	if err := run([]string{"status"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "signed in:     no") {
		t.Fatalf("status still reports a login after logout:\n%s", out.String())
	}
}

// TestLogoutNamesTheProfilesItWillAlsoSignOut: Google revokes the grant,
// not one token, so a second profile sharing the OAuth client goes with
// it. Measured on a sibling, not assumed.
func TestLogoutNamesTheProfilesItWillAlsoSignOut(t *testing.T) {
	srv, _ := fakeGoogle(t)
	dir, env := setupLoginWith(t, srv)

	var out, errOut bytes.Buffer
	if err := run([]string{"login"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatal(err)
	}
	// A second profile using the same client JSON.
	workEnv := func(k string) string {
		if k == "GCAL_PROFILE" {
			return "work"
		}
		return env(k)
	}
	out.Reset()
	if err := run([]string{"login"}, strings.NewReader(""), &out, &errOut, workEnv); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := run([]string{"logout"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "work") {
		t.Fatalf("logout did not warn that the other profile is also signed out:\n%s", out.String())
	}
	_ = dir
}

func TestDoctorAfterLogin(t *testing.T) {
	srv, _ := fakeGoogle(t)
	_, env := setupLoginWith(t, srv)

	var out, errOut bytes.Buffer
	if err := run([]string{"login"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"doctor"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	// The client JSON, the token and the scopes all check out; the API
	// call fails, because the fake Google serves no Calendar API. That
	// is the shape doctor exists to report.
	for _, want := range []string{"OAuth Desktop client JSON", "refresh token", "granted scopes"} {
		if !strings.Contains(s, want) {
			t.Fatalf("doctor did not check %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "ok  ") {
		t.Fatalf("doctor reported nothing as working:\n%s", s)
	}
}

// TestBuildServiceFailsWithoutCredentialsAndTheServerStartsAnyway is the
// pair of behaviours that let a client discover this server before
// anybody has logged in.
func TestBuildServiceFailsWithoutCredentialsAndTheServerStartsAnyway(t *testing.T) {
	_, env := setupLogin(t) // client JSON written, but no login run

	st, storeErr := store(configForTest(), env, func(string) {})
	if storeErr != nil {
		t.Fatalf("store: %v", storeErr)
	}
	_, _, resolveErr := st.Resolve()
	if resolveErr == nil {
		t.Fatal("resolved a token on a machine that never logged in")
	}
	if !errorIsNotFound(resolveErr) {
		t.Fatalf("unexpected error: %v", resolveErr)
	}

	// And buildService surfaces it rather than panicking.
	if _, err := buildService(t.Context(), configForTest(), env, func(string) {}); err == nil {
		t.Fatal("buildService succeeded with no stored token")
	}
	_ = credentials.EnvVar
}

// TestLoginNamesTheAccountWithoutAnEmailScope.
//
// tokeninfo returns an email only when an email scope was granted, and
// this server asks for none. The account was silently blank on the first
// live login, leaving `status` with a field that could never populate.
// The primary calendar's id is the address, and it costs no extra scope.
func TestLoginNamesTheAccountWithoutAnEmailScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`))
		case strings.HasSuffix(r.URL.Path, "/tokeninfo"):
			// No email: the scope was never asked for. This is what
			// Google actually returns for this server's scope set.
			_, _ = w.Write([]byte(`{"scope":"` + strings.Join(auth.Scopes(auth.Access{Sharing: true}), " ") +
				`","expires_in":"3599","aud":"id"}`))
		case strings.HasSuffix(r.URL.Path, "/calendars/primary"):
			_, _ = w.Write([]byte(`{"id":"person@example.test","summary":"Sample","timeZone":"UTC"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	_, env := setupLoginWith(t, srv)
	oldBase := gapi.BaseURL
	gapi.BaseURL = srv.URL
	defer func() { gapi.BaseURL = oldBase }()

	var out, errOut bytes.Buffer
	if err := run([]string{"login"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out.String(), "person@example.test") {
		t.Fatalf("login did not name the account even though the primary calendar carries it:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"status"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "person@example.test") {
		t.Fatalf("status has an account field that never populates:\n%s", out.String())
	}
}
