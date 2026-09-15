// Package auth implements Google's documented OAuth flow for desktop
// applications: a loopback redirect on 127.0.0.1 with a random port and
// PKCE, then a refresh-token-backed token source for API calls.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// OAuth scopes, narrow by design.
//
// The broad `calendar` scope is deliberately absent. It grants
// everything this API can do, including deleting a calendar and clearing
// the primary one — the two operations §9 keeps behind an env flag — so
// asking for it would make the flag a label rather than a limit. The
// narrow scopes below add up to the tool surface and nothing more.
const (
	// ScopeReadonly covers reading calendars, the subscribed list and
	// events. It does NOT cover acl.list; see ScopeACLReadonly.
	ScopeReadonly = "https://www.googleapis.com/auth/calendar.readonly"
	// ScopeSettingsReadonly covers settings.list, which is where the
	// user's own time zone lives — the last resort in §4.1's resolution
	// order.
	ScopeSettingsReadonly = "https://www.googleapis.com/auth/calendar.settings.readonly"
	// ScopeACLReadonly covers acl.list.
	//
	// It is a separate scope rather than an oversight. Verified against
	// the discovery document (§2.15): acl.get lists calendar.readonly
	// among its scopes and acl.list does not. Reading one sharing rule
	// and reading the list of them take different grants, which surfaces
	// as a 403 from one tool weeks after a working setup.
	ScopeACLReadonly = "https://www.googleapis.com/auth/calendar.acls.readonly"

	// ScopeEvents covers events.insert, patch, delete, move and import.
	ScopeEvents = "https://www.googleapis.com/auth/calendar.events"
	// ScopeCalendars covers calendars.insert, patch, delete and clear.
	ScopeCalendars = "https://www.googleapis.com/auth/calendar.calendars"
	// ScopeCalendarList covers subscribing, unsubscribing and the
	// per-user overrides.
	ScopeCalendarList = "https://www.googleapis.com/auth/calendar.calendarlist"
	// ScopeACL covers acl.insert, patch and delete.
	ScopeACL = "https://www.googleapis.com/auth/calendar.acls"
)

// Access describes what a login should ask for. It mirrors the config
// flags rather than being derived from them, so the scope set is a
// value a test can build and the doctor command can compare.
type Access struct {
	// ReadOnly asks only for the read scopes.
	ReadOnly bool
	// Sharing includes the ACL scopes. The read half is included even
	// when sharing is off, because get_calendar shows exposure.
	Sharing bool
}

// Scopes returns the scope set for the requested access level, in a
// stable order so that two logins with the same access produce the same
// consent screen and the same stored list.
//
// Generated from this function rather than written into the
// documentation by hand: the standard's rule is that a scope list a
// person must paste into a consent screen is an input, not a
// description, so docs/gcp-setup.md is produced from here and gated.
func Scopes(a Access) []string {
	out := []string{ScopeReadonly, ScopeSettingsReadonly}
	if a.Sharing {
		out = append(out, ScopeACLReadonly)
	}
	if a.ReadOnly {
		return out
	}
	out = append(out, ScopeEvents, ScopeCalendars, ScopeCalendarList)
	if a.Sharing {
		out = append(out, ScopeACL)
	}
	return out
}

// ErrNotDesktopClient means the JSON is not a "Desktop app" OAuth client.
var ErrNotDesktopClient = errors.New(`auth: client secret JSON is not a Desktop app client (expected an "installed" section)`)

// Google's OAuth endpoints, used when the client JSON omits them.
const (
	GoogleAuthURL = "https://accounts.google.com/o/oauth2/auth"
	// An endpoint address, not a credential; the scanner matches on the
	// word "token" in the name.
	GoogleTokenURL = "https://oauth2.googleapis.com/token" //nolint:gosec // a public endpoint URL
)

// LoadClientSecret reads a Desktop-app client JSON downloaded from the
// Google Cloud console and returns an oauth2.Config for the scopes.
func LoadClientSecret(path string, scopes []string) (*oauth2.Config, error) {
	// The path is the person's own OAuth client JSON, named by them on
	// the command line or in their profile. Reading the file they asked
	// for is the whole point of the function.
	data, err := os.ReadFile(path) //nolint:gosec // a path the operator supplied deliberately
	if err != nil {
		return nil, fmt.Errorf("auth: read client secret %s: %w", path, err)
	}
	return ParseClientSecret(data, scopes)
}

// ParseClientSecret is LoadClientSecret on bytes.
func ParseClientSecret(data []byte, scopes []string) (*oauth2.Config, error) {
	var file struct {
		Installed *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			AuthURI      string `json:"auth_uri"`
			TokenURI     string `json:"token_uri"`
		} `json:"installed"`
		Web json.RawMessage `json:"web"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("auth: client secret is not valid JSON: %w", err)
	}
	if file.Installed == nil {
		if len(file.Web) > 0 {
			return nil, fmt.Errorf("%w: this is a Web application client; create a Desktop app client instead", ErrNotDesktopClient)
		}
		return nil, ErrNotDesktopClient
	}
	in := file.Installed
	if in.ClientID == "" {
		return nil, errors.New("auth: client secret JSON has no client_id")
	}
	authURL, tokenURL := in.AuthURI, in.TokenURI
	if authURL == "" {
		authURL = GoogleAuthURL
	}
	if tokenURL == "" {
		tokenURL = GoogleTokenURL
	}
	return &oauth2.Config{
		ClientID:     in.ClientID,
		ClientSecret: in.ClientSecret,
		Scopes:       scopes,
		Endpoint:     oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}, nil
}

// LoginOptions tune the interactive flow. Zero values are sensible.
type LoginOptions struct {
	// OpenBrowser is called with the authorization URL. nil uses the OS
	// default browser; a failure is not fatal, since the URL is printed
	// to Out as well.
	OpenBrowser func(url string) error
	// NoBrowser prints the URL and does not try to open anything. It is
	// what makes this flow usable over SSH, where the callback reaches
	// the remote host's loopback and the browser is local.
	NoBrowser bool
	// Out receives the URL and progress messages. nil discards them.
	Out io.Writer
	// Timeout bounds the person's trip through the browser. Default 5m.
	Timeout time.Duration
	// Listener overrides the loopback listener (tests).
	Listener net.Listener
	// HTTPTimeout bounds the code exchange. Zero means
	// DefaultHTTPTimeout. It is not Timeout: one bounds a person, the
	// other bounds a server.
	HTTPTimeout time.Duration
}

// Login runs the loopback authorization-code flow and returns a token
// that includes a refresh token.
//
// The listener is the IP literal 127.0.0.1 on a random port, not
// "localhost": RFC 8252 §7.3 says the literal "avoids inadvertently
// listening on network interfaces other than the loopback interface"
// and is "less susceptible to client-side firewalls and misconfigured
// host name resolution".
func Login(ctx context.Context, cfg *oauth2.Config, opts LoginOptions) (*oauth2.Token, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ln := opts.Listener
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("auth: listen on loopback: %w", err)
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port

	conf := *cfg
	conf.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	state, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()
	authURL := conf.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier),
	)

	type outcome struct {
		code string
		err  error
	}
	results := make(chan outcome, 1)
	deliver := func(o outcome) {
		select {
		case results <- o:
		default:
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch; start the login again", http.StatusBadRequest)
			deliver(outcome{err: errors.New("auth: state mismatch on callback")})
			return
		}
		if e := q.Get("error"); e != "" {
			http.Error(w, "authorization failed: "+e, http.StatusBadRequest)
			deliver(outcome{err: fmt.Errorf("auth: authorization denied: %s", e)})
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			deliver(outcome{err: errors.New("auth: callback without code")})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, successPage)
		deliver(outcome{code: code})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	_, _ = fmt.Fprintf(out, "Open this URL in your browser to authorize google-calendar-mcp:\n\n%s\n\n", authURL)
	if opts.NoBrowser {
		// Said here rather than only in the README, because this is the
		// moment somebody on a remote host finds out it does not work.
		_, _ = fmt.Fprintf(out, "The callback goes to 127.0.0.1:%d on THIS machine. If your browser is "+
			"somewhere else, forward the port first:\n\n    ssh -L %d:127.0.0.1:%d <this-host>\n\n", port, port, port)
	} else {
		open := opts.OpenBrowser
		if open == nil {
			open = OpenBrowser
		}
		if err := open(authURL); err != nil {
			_, _ = fmt.Fprintf(out, "(could not open a browser automatically: %v)\n", err)
		}
	}
	_, _ = fmt.Fprintln(out, "Waiting for the browser to finish...")

	var code string
	select {
	case o := <-results:
		if o.err != nil {
			return nil, o.err
		}
		code = o.code
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, errors.New("auth: timed out waiting for the browser")
	}

	tok, err := conf.Exchange(boundHTTP(ctx, opts.HTTPTimeout), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("auth: exchange code: %w", err)
	}
	if tok.RefreshToken == "" {
		return nil, errors.New("auth: Google returned no refresh token; remove the app at https://myaccount.google.com/permissions and log in again")
	}
	return tok, nil
}

// DefaultHTTPTimeout bounds an OAuth call when no timeout is given.
const DefaultHTTPTimeout = 60 * time.Second

// boundHTTP puts a client with a timeout in the context.
//
// Without one the oauth2 library uses http.DefaultClient, which has no
// timeout: a token endpoint that accepts the connection and never
// answers would hang the first tool call for as long as the process
// runs. The API client's own per-request timeout does not cover this,
// because the refresh happens inside the token source rather than on
// that client.
func boundHTTP(ctx context.Context, timeout time.Duration) context.Context {
	if timeout <= 0 {
		timeout = DefaultHTTPTimeout
	}
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: timeout})
}

// TokenSource returns a caching token source backed by the refresh
// token. timeout bounds each refresh; zero means DefaultHTTPTimeout.
func TokenSource(ctx context.Context, cfg *oauth2.Config, refreshToken string, timeout time.Duration) oauth2.TokenSource {
	ctx = boundHTTP(ctx, timeout)
	return oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}))
}

// Endpoints used for revocation and token inspection. Vars so tests can
// point them at a local server.
var (
	RevokeURL = "https://oauth2.googleapis.com/revoke"
	// TokenInfoURL is an endpoint address, not a credential; the scanner
	// matches on the word "token" in the name.
	TokenInfoURL = "https://oauth2.googleapis.com/tokeninfo" //nolint:gosec // a public endpoint URL
)

// Revoke invalidates a refresh (or access) token at Google.
func Revoke(ctx context.Context, client *http.Client, token string) error {
	if client == nil {
		client = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("auth: revoke: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("auth: revoke returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// TokenInfo describes an access token as Google sees it.
type TokenInfo struct {
	Scopes    []string
	Email     string
	ExpiresIn time.Duration
	Audience  string
}

// Inspect calls the tokeninfo endpoint for an access token.
func Inspect(ctx context.Context, client *http.Client, accessToken string) (*TokenInfo, error) {
	if client == nil {
		client = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	u := TokenInfoURL + "?access_token=" + url.QueryEscape(accessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: tokeninfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: tokeninfo returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		Scope     string `json:"scope"`
		Email     string `json:"email"`
		ExpiresIn string `json:"expires_in"`
		Aud       string `json:"aud"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("auth: tokeninfo: %w", err)
	}
	info := &TokenInfo{Email: raw.Email, Audience: raw.Aud}
	if raw.Scope != "" {
		info.Scopes = strings.Fields(raw.Scope)
	}
	if secs, err := strconv.Atoi(raw.ExpiresIn); err == nil {
		info.ExpiresIn = time.Duration(secs) * time.Second
	}
	return info, nil
}

// MissingScopes returns the wanted scopes that were not granted. It is
// what doctor uses to turn a future 403 into a sentence at setup time.
func MissingScopes(granted, wanted []string) []string {
	have := make(map[string]bool, len(granted))
	for _, s := range granted {
		have[s] = true
	}
	var missing []string
	for _, w := range wanted {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// OpenBrowser opens url with the platform's default handler.
func OpenBrowser(u string) error {
	// u is the authorization URL this package just built from the OAuth
	// config; it is not attacker-controlled. exec.Command passes it as a
	// single argument with no shell, so there is nothing to inject into.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u) //nolint:gosec // our own URL, no shell
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u) //nolint:gosec // our own URL, no shell
	default:
		cmd = exec.Command("xdg-open", u) //nolint:gosec // our own URL, no shell
	}
	return cmd.Start()
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

const successPage = `<!doctype html><meta charset="utf-8"><title>google-calendar-mcp</title>
<body style="font-family:system-ui;margin:3rem"><h2>Signed in</h2>
<p>google-calendar-mcp received the authorization. You can close this window.</p></body>`
