package auth_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mmedum/google-calendar-mcp/v2/internal/auth"
)

// TestScopesNeverIncludeTheBroadScope is §10 and §9 together: the broad
// `calendar` scope grants the two operations kept behind an env flag, so
// asking for it would make the flag a label.
func TestScopesNeverIncludeTheBroadScope(t *testing.T) {
	const broad = "https://www.googleapis.com/auth/calendar"
	for _, a := range []auth.Access{
		{}, {ReadOnly: true}, {Sharing: true}, {ReadOnly: true, Sharing: true},
	} {
		for _, s := range auth.Scopes(a) {
			if s == broad {
				t.Fatalf("Scopes(%+v) asked for the broad calendar scope", a)
			}
		}
	}
}

// TestReadOnlyAsksForNoWriteScope: read-only is a real restriction, not
// a label on the tool list.
func TestReadOnlyAsksForNoWriteScope(t *testing.T) {
	writeScopes := map[string]bool{
		auth.ScopeEvents: true, auth.ScopeCalendars: true,
		auth.ScopeCalendarList: true, auth.ScopeACL: true,
	}
	for _, a := range []auth.Access{{ReadOnly: true}, {ReadOnly: true, Sharing: true}} {
		for _, s := range auth.Scopes(a) {
			if writeScopes[s] {
				t.Fatalf("read-only login asked for the write scope %s", s)
			}
		}
	}
}

// TestACLReadNeedsItsOwnScope is §2.15, verified against the discovery
// document: calendar.readonly does not cover acl.list.
func TestACLReadNeedsItsOwnScope(t *testing.T) {
	with := auth.Scopes(auth.Access{Sharing: true})
	if !contains(with, auth.ScopeACLReadonly) {
		t.Fatal("sharing login does not ask for calendar.acls.readonly; acl.list is not covered by calendar.readonly")
	}
	without := auth.Scopes(auth.Access{Sharing: false})
	if contains(without, auth.ScopeACLReadonly) || contains(without, auth.ScopeACL) {
		t.Fatal("sharing is off, so neither ACL scope should be requested")
	}
}

func TestScopesAreStableAndDeduplicated(t *testing.T) {
	for _, a := range []auth.Access{{}, {ReadOnly: true}, {Sharing: true}, {ReadOnly: true, Sharing: true}} {
		first := auth.Scopes(a)
		second := auth.Scopes(a)
		if strings.Join(first, " ") != strings.Join(second, " ") {
			t.Fatalf("Scopes(%+v) is not stable between calls", a)
		}
		seen := map[string]bool{}
		for _, s := range first {
			if seen[s] {
				t.Fatalf("Scopes(%+v) repeats %s", a, s)
			}
			seen[s] = true
		}
		if len(first) == 0 {
			t.Fatalf("Scopes(%+v) is empty", a)
		}
	}
}

func TestParseClientSecret(t *testing.T) {
	good := `{"installed":{"client_id":"id.apps.googleusercontent.com","client_secret":"s","auth_uri":"https://a","token_uri":"https://t"}}`
	cfg, err := auth.ParseClientSecret([]byte(good), []string{"scope"})
	if err != nil {
		t.Fatalf("ParseClientSecret: %v", err)
	}
	if cfg.ClientID != "id.apps.googleusercontent.com" || cfg.Endpoint.AuthURL != "https://a" {
		t.Fatalf("parsed wrong: %+v", cfg)
	}

	// Endpoints default when the file omits them.
	cfg, err = auth.ParseClientSecret([]byte(`{"installed":{"client_id":"x"}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint.AuthURL != auth.GoogleAuthURL || cfg.Endpoint.TokenURL != auth.GoogleTokenURL {
		t.Fatalf("endpoints did not default: %+v", cfg.Endpoint)
	}

	// A Web client is the common mistake and gets its own message.
	_, err = auth.ParseClientSecret([]byte(`{"web":{"client_id":"x"}}`), nil)
	if !errors.Is(err, auth.ErrNotDesktopClient) {
		t.Fatalf("web client error = %v, want ErrNotDesktopClient", err)
	}
	if !strings.Contains(err.Error(), "Desktop app client instead") {
		t.Fatalf("web client error does not say what to do: %v", err)
	}

	for _, bad := range []string{`not json`, `{}`, `{"installed":{}}`} {
		if _, err := auth.ParseClientSecret([]byte(bad), nil); err == nil {
			t.Fatalf("ParseClientSecret(%q) succeeded", bad)
		}
	}
}

// TestLoginUsesLoopbackLiteralAndPKCE checks the two properties RFC 8252
// requires, by driving the whole flow against a fake Google.
func TestLoginUsesLoopbackLiteralAndPKCE(t *testing.T) {
	var gotRedirect, gotChallengeMethod, gotChallenge string

	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotRedirect = r.Form.Get("redirect_uri")
		if r.Form.Get("code_verifier") == "" {
			t.Error("token exchange carried no code_verifier")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`))
	}))
	defer token.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &oauth2.Config{
		ClientID: "id", ClientSecret: "secret",
		Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example.test/auth", TokenURL: token.URL, AuthStyle: oauth2.AuthStyleInParams},
	}

	// The "browser": parse the URL, check PKCE, then call back.
	browser := func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		q := u.Query()
		gotChallengeMethod = q.Get("code_challenge_method")
		gotChallenge = q.Get("code_challenge")
		go func() {
			cb := q.Get("redirect_uri") + "?state=" + url.QueryEscape(q.Get("state")) + "&code=authcode"
			resp, err := http.Get(cb) //nolint:noctx // test callback
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	tok, err := auth.Login(context.Background(), cfg, auth.LoginOptions{
		Listener: ln, OpenBrowser: browser, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.RefreshToken != "rt" {
		t.Fatalf("refresh token = %q", tok.RefreshToken)
	}
	if gotChallengeMethod != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256 (RFC 8252 §8.1)", gotChallengeMethod)
	}
	if gotChallenge == "" {
		t.Fatal("no code_challenge was sent")
	}
	if !strings.HasPrefix(gotRedirect, "http://127.0.0.1:") {
		t.Fatalf("redirect_uri = %q, want the 127.0.0.1 literal (RFC 8252 §7.3), never localhost", gotRedirect)
	}
	if strings.Contains(gotRedirect, "localhost") {
		t.Fatalf("redirect_uri used localhost: %q", gotRedirect)
	}
}

func TestLoginRejectsAStateMismatch(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{AuthURL: "https://a", TokenURL: "https://t"}}
	browser := func(raw string) error {
		u, _ := url.Parse(raw)
		go func() {
			cb := u.Query().Get("redirect_uri") + "?state=wrong&code=authcode"
			resp, err := http.Get(cb) //nolint:noctx // test callback
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	_, err = auth.Login(context.Background(), cfg, auth.LoginOptions{
		Listener: ln, OpenBrowser: browser, Timeout: 5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("Login accepted a mismatched state: %v", err)
	}
}

// TestNoBrowserPrintsThePortToForward: the SSH case, which the standard
// says to give people rather than let them discover.
func TestNoBrowserPrintsThePortToForward(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	var out strings.Builder
	cfg := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{AuthURL: "https://a", TokenURL: "https://t"}}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _ = auth.Login(ctx, cfg, auth.LoginOptions{Listener: ln, NoBrowser: true, Out: &out, Timeout: time.Minute})

	s := out.String()
	if !strings.Contains(s, "ssh -L") {
		t.Fatalf("--no-browser did not print the port-forward line:\n%s", s)
	}
	if !strings.Contains(s, "127.0.0.1:"+itoa(port)) {
		t.Fatalf("--no-browser did not print the actual port %d:\n%s", port, s)
	}
}

func TestMissingScopes(t *testing.T) {
	granted := []string{"a", "b"}
	if got := auth.MissingScopes(granted, []string{"a", "b"}); len(got) != 0 {
		t.Fatalf("MissingScopes = %v, want none", got)
	}
	got := auth.MissingScopes(granted, []string{"a", "c", "d"})
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("MissingScopes = %v, want [c d]", got)
	}
}

func TestInspectAndRevoke(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "revoke") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scope":"a b","email":"someone@example.test","expires_in":"3599","aud":"id"}`))
	}))
	defer srv.Close()

	oldInfo, oldRevoke := auth.TokenInfoURL, auth.RevokeURL
	auth.TokenInfoURL, auth.RevokeURL = srv.URL+"/tokeninfo", srv.URL+"/revoke"
	defer func() { auth.TokenInfoURL, auth.RevokeURL = oldInfo, oldRevoke }()

	info, err := auth.Inspect(context.Background(), srv.Client(), "at")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(info.Scopes) != 2 || info.Email != "someone@example.test" || info.ExpiresIn != 3599*time.Second {
		t.Fatalf("Inspect = %+v", info)
	}
	if err := auth.Revoke(context.Background(), srv.Client(), "rt"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestInspectDoesNotLeakTheTokenOnATransportError.
//
// The access token is a query parameter on the tokeninfo URL, and a
// transport failure stringifies the whole URL. `doctor` prints that
// error, and `doctor` output is what a user pastes into a bug report.
func TestInspectDoesNotLeakTheTokenOnATransportError(t *testing.T) {
	const token = "ya29.a-secret-access-token"
	// A client that always fails the way a dead network does.
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	})}
	_, err := auth.Inspect(context.Background(), client, token)
	if err == nil {
		t.Fatal("a dead transport produced no error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("the access token is in the error text: %v", err)
	}
	if strings.Contains(err.Error(), "access_token") {
		t.Fatalf("the token-bearing URL is in the error text: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
