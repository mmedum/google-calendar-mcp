package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/mmedum/google-calendar-mcp/internal/auth"
	"github.com/mmedum/google-calendar-mcp/internal/config"
	"github.com/mmedum/google-calendar-mcp/internal/credentials"
	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/service"
	"github.com/mmedum/google-calendar-mcp/internal/userconfig"
)

// keyringBackend is the credential store's keyring.
//
// A var so a test can substitute an in-memory one. This is not a
// convenience: `go test ./...` with the real backend deletes the
// maintainer's own refresh token, which is exactly how a sibling lost
// its token five times in one session — the test isolated the config
// directory and the environment, and could not isolate the OS keyring.
var keyringBackend = credentials.OSKeyring()

// openBrowser is how login reaches a browser. nil means the platform's
// default handler; a test substitutes the callback itself.
var openBrowser func(string) error

// accessFor turns the configuration into the scope set to request.
func accessFor(cfg config.Config) auth.Access {
	return auth.Access{ReadOnly: cfg.ReadOnly, Sharing: cfg.Sharing}
}

// store builds the credential store for a profile.
func store(cfg config.Config, env func(string) string, warn func(string)) (*credentials.Store, error) {
	tokenPath, err := userconfig.TokenFilePath(cfg.Profile)
	if err != nil {
		return nil, err
	}
	uc, err := userconfig.Load(cfg.Profile)
	if err != nil && !errors.Is(err, userconfig.ErrNotFound) {
		return nil, err
	}
	return &credentials.Store{
		Profile: cfg.Profile, Keyring: keyringBackend,
		FilePath: tokenPath, Env: env, Warn: warn,
		ExpectKeyring: uc.TokenStore == string(credentials.SourceKeyring),
	}, nil
}

// buildService assembles the authenticated service the server uses.
func buildService(ctx context.Context, cfg config.Config, env func(string) string, warn func(string)) (*service.Service, error) {
	st, err := store(cfg, env, warn)
	if err != nil {
		return nil, err
	}
	refresh, _, err := st.Resolve()
	if err != nil {
		return nil, err
	}
	secretPath, err := userconfig.ResolveClientSecretPath(cfg.Profile, cfg.ClientSecretPath)
	if err != nil {
		return nil, err
	}
	oc, err := auth.LoadClientSecret(secretPath, auth.Scopes(accessFor(cfg)))
	if err != nil {
		return nil, err
	}
	ts := auth.TokenSource(ctx, oc, refresh, cfg.HTTPTimeout)
	hc := oauthClient(ctx, ts, cfg.HTTPTimeout)
	api := gapi.New(hc)
	return service.New(api, cfg), nil
}

// subConfig parses the shared flags for a subcommand.
func subConfig(name string, args []string, out io.Writer, env func(string) string) (config.Config, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	settings := config.Define(fs, env)
	noBrowser := fs.Bool("no-browser", false, "print the authorization URL instead of opening a browser")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, nil, err
	}
	cfg, err := settings.Build()
	if err != nil {
		return config.Config{}, nil, err
	}
	// Carried on the FlagSet so cmdLogin can read it without a second
	// parse; nothing else needs it.
	if *noBrowser {
		_ = fs.Set("no-browser", "true")
	}
	return cfg, fs, nil
}

func flagBool(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	return f != nil && f.Value.String() == "true"
}

// cmdLogin runs the OAuth flow and stores the refresh token.
func cmdLogin(args []string, stdout, stderr io.Writer, env func(string) string) error {
	cfg, fs, err := subConfig("login", args, stderr, env)
	if err != nil {
		return err
	}
	secretPath, err := userconfig.ResolveClientSecretPath(cfg.Profile, cfg.ClientSecretPath)
	if err != nil {
		return err
	}
	scopes := auth.Scopes(accessFor(cfg))
	oc, err := auth.LoadClientSecret(secretPath, scopes)
	if err != nil {
		return fmt.Errorf("%w\n\nCreate an OAuth *Desktop app* client in the Google Cloud console, download "+
			"the JSON, and pass it with --client-secret or put it at %s", err, secretPath)
	}

	_, _ = fmt.Fprintf(stdout, "Signing in to profile %q, asking for %d scopes:\n", cfg.Profile, len(scopes))
	for _, s := range scopes {
		_, _ = fmt.Fprintf(stdout, "  %s\n", s)
	}
	_, _ = fmt.Fprintln(stdout)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	tok, err := auth.Login(ctx, oc, auth.LoginOptions{
		Out: stdout, NoBrowser: flagBool(fs, "no-browser"),
		HTTPTimeout: cfg.HTTPTimeout, OpenBrowser: openBrowser,
	})
	if err != nil {
		return err
	}

	st, err := store(cfg, env, func(m string) { _, _ = fmt.Fprintf(stderr, "warning: %s\n", m) })
	if err != nil {
		return err
	}
	src, err := st.Save(tok.RefreshToken)
	if err != nil {
		return err
	}

	uc, err := userconfig.Load(cfg.Profile)
	if err != nil && !errors.Is(err, userconfig.ErrNotFound) {
		return err
	}
	uc.ClientSecretPath = secretPath
	uc.TokenStore = string(src)

	// The profile records what Google GRANTED, not what this server
	// asked for. They are different things and the difference is the
	// whole point: a scope that was requested and refused is exactly the
	// failure worth seeing, and storing the request would present it as
	// a fact about the grant. `status` prints this list.
	uc.Scopes = scopes
	if info, err := auth.Inspect(ctx, nil, tok.AccessToken); err == nil {
		uc.AccountEmail = info.Email
		if len(info.Scopes) > 0 {
			uc.Scopes = info.Scopes
		}
		if missing := auth.MissingScopes(info.Scopes, scopes); len(missing) > 0 {
			_, _ = fmt.Fprintf(stderr, "warning: these scopes were not granted, so some tools will fail:\n  %s\n",
				strings.Join(missing, "\n  "))
		}
	}

	// tokeninfo returns an email only when an email scope was granted,
	// and this server asks for none — it has no business reading the
	// person's profile. So the account was silently blank, and `status`
	// had a field that could never populate. Observed on the first live
	// login.
	//
	// The primary calendar's id IS the account's address, and reading it
	// needs nothing beyond the scopes already granted.
	if uc.AccountEmail == "" {
		if email, err := primaryAddress(ctx, tok, cfg); err == nil {
			uc.AccountEmail = email
		}
	}
	if err := userconfig.Save(cfg.Profile, uc); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "\nSigned in. Refresh token stored in the %s.\n", src)
	if uc.AccountEmail != "" {
		_, _ = fmt.Fprintf(stdout, "Account: %s\n", uc.AccountEmail)
	}
	return nil
}

// primaryAddress reads the account's own address from its primary
// calendar's id, which is what Google uses for a person's own calendar.
//
// It costs one request and no extra scope, which is why it is preferred
// over asking for userinfo.email at the consent screen: a calendar
// server asking to read your profile is a scope nobody should have to
// grant to see their own schedule.
func primaryAddress(ctx context.Context, tok *oauth2.Token, cfg config.Config) (string, error) {
	// The access token just issued is enough on its own: this call
	// happens inside login, well before the token could expire, so it
	// needs no refreshing token source and therefore no oauth2.Config.
	ts := oauth2.StaticTokenSource(tok)
	api := gapi.New(oauthClient(ctx, ts, cfg.HTTPTimeout))
	cal, err := api.GetCalendar(ctx, "primary")
	if err != nil {
		return "", err
	}
	if !strings.Contains(cal.ID, "@") {
		return "", errors.New("the primary calendar's id is not an address")
	}
	return cal.ID, nil
}

// cmdLogout revokes the grant and removes the stored token.
func cmdLogout(args []string, stdout io.Writer, env func(string) string) error {
	cfg, _, err := subConfig("logout", args, stdout, env)
	if err != nil {
		return err
	}
	st, err := store(cfg, env, func(string) {})
	if err != nil {
		return err
	}

	// Google revokes the GRANT, not one token, so this signs the account
	// out of every profile sharing the same OAuth client. Say so before
	// doing it rather than after.
	if others, err := userconfig.SharingClient(cfg.Profile); err == nil && len(others) > 0 {
		_, _ = fmt.Fprintf(stdout, "Note: these profiles use the same OAuth client and will also be signed out: %s\n",
			strings.Join(others, ", "))
	}

	token, _, err := st.ResolveStored()
	switch {
	case err == nil:
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if rerr := auth.Revoke(ctx, nil, token); rerr != nil {
			_, _ = fmt.Fprintf(stdout, "Could not revoke the token at Google (%v); removing the local copy anyway.\n", rerr)
		} else {
			_, _ = fmt.Fprintln(stdout, "Revoked the grant at Google.")
		}
	case errors.Is(err, credentials.ErrNotFound):
		_, _ = fmt.Fprintln(stdout, "No stored token to revoke.")
	default:
		_, _ = fmt.Fprintf(stdout, "Could not read the stored token (%v); removing what is there.\n", err)
	}

	if err := st.Delete(); err != nil {
		return err
	}
	if err := userconfig.Remove(cfg.Profile); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Signed out of profile %q.\n", cfg.Profile)
	return nil
}

// oauthClient wraps a token source in an HTTP client with a timeout.
//
// oauth2.NewClient's transport has no timeout of its own, so the timeout
// is set on the client it returns. Without it a Google endpoint that
// accepts the connection and never answers hangs a tool call for as long
// as the process runs.
func oauthClient(ctx context.Context, ts oauth2.TokenSource, timeout time.Duration) *http.Client {
	hc := oauth2.NewClient(ctx, ts)
	if timeout <= 0 {
		timeout = auth.DefaultHTTPTimeout
	}
	hc.Timeout = timeout
	return hc
}
