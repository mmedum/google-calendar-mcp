package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/v2/internal/auth"
	"github.com/mmedum/google-calendar-mcp/v2/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v2/internal/userconfig"
	"github.com/mmedum/google-calendar-mcp/v2/internal/version"
)

// cmdStatus prints what this profile is configured with. It touches no
// network.
func cmdStatus(args []string, stdout io.Writer, env func(string) string) error {
	cfg, _, err := subConfig("status", args, stdout, env)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "%s\n\n", version.Info())
	_, _ = fmt.Fprintf(stdout, "profile:       %s\n", cfg.Profile)

	dir, err := userconfig.ProfileDir(cfg.Profile)
	if err == nil {
		_, _ = fmt.Fprintf(stdout, "config dir:    %s\n", dir)
	}

	uc, err := userconfig.Load(cfg.Profile)
	switch {
	case errors.Is(err, userconfig.ErrNotFound):
		_, _ = fmt.Fprintln(stdout, "signed in:     no — run `google-calendar-mcp login`")
	case err != nil:
		return err
	default:
		_, _ = fmt.Fprintf(stdout, "signed in:     yes\n")
		if uc.AccountEmail != "" {
			_, _ = fmt.Fprintf(stdout, "account:       %s\n", uc.AccountEmail)
		}
		_, _ = fmt.Fprintf(stdout, "token store:   %s\n", uc.TokenStore)
		_, _ = fmt.Fprintf(stdout, "client JSON:   %s\n", uc.ClientSecretPath)
		if !uc.UpdatedAt.IsZero() {
			_, _ = fmt.Fprintf(stdout, "last login:    %s\n", uc.UpdatedAt.Format(time.RFC3339))
		}
		if len(uc.Scopes) > 0 {
			_, _ = fmt.Fprintf(stdout, "scopes:\n  %s\n", strings.Join(uc.Scopes, "\n  "))
		}
	}

	_, _ = fmt.Fprintf(stdout, "\nread-only:     %t\n", cfg.ReadOnly)
	_, _ = fmt.Fprintf(stdout, "sharing tools: %t\n", cfg.Sharing)
	_, _ = fmt.Fprintf(stdout, "destructive:   %t\n", cfg.EnableDestructive)
	_, _ = fmt.Fprintf(stdout, "event budget:  %d\n", cfg.MaxEvents)
	_, _ = fmt.Fprintf(stdout, "max calendars: %d\n", cfg.MaxCalendars)

	if names, err := userconfig.Profiles(); err == nil && len(names) > 1 {
		_, _ = fmt.Fprintf(stdout, "\nother profiles: %s\n", strings.Join(names, ", "))
	}
	return nil
}

// cmdDoctor checks the things that actually go wrong at setup and names
// what is missing.
//
// Most first-run reports are a missing API, a consent screen, or a scope
// that was never granted. Each check below prints a line that says what
// to do, because a diagnostic that only says "failed" moves the work
// back to the person reporting it.
func cmdDoctor(args []string, stdout io.Writer, env func(string) string) error {
	cfg, _, err := subConfig("doctor", args, stdout, env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, _ = fmt.Fprintf(stdout, "%s\nprofile: %s\n\n", version.Info(), cfg.Profile)
	problems := 0
	report := func(ok bool, label, detail string) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			problems++
		}
		_, _ = fmt.Fprintf(stdout, "[%s] %s\n", mark, label)
		if detail != "" {
			_, _ = fmt.Fprintf(stdout, "       %s\n", detail)
		}
	}

	// 1. The OAuth client JSON.
	secretPath, err := userconfig.ResolveClientSecretPath(cfg.Profile, cfg.ClientSecretPath)
	if err != nil {
		return err
	}
	wanted := auth.Scopes(accessFor(cfg))
	oc, err := auth.LoadClientSecret(secretPath, wanted)
	if err != nil {
		report(false, "OAuth Desktop client JSON", fmt.Sprintf("%v\n       expected at %s", err, secretPath))
		_, _ = fmt.Fprintf(stdout, "\n%d problem(s).\n", problems)
		return nil
	}
	report(true, "OAuth Desktop client JSON", secretPath)

	// 2. A stored refresh token.
	st, err := store(cfg, env, func(m string) { _, _ = fmt.Fprintf(stdout, "       warning: %s\n", m) })
	if err != nil {
		return err
	}
	refresh, src, err := st.Resolve()
	if err != nil {
		report(false, "refresh token", err.Error())
		_, _ = fmt.Fprintf(stdout, "\n%d problem(s).\n", problems)
		return nil
	}
	report(true, "refresh token", "from the "+string(src))

	// 3. The token still works, and carries the scopes the tool surface
	//    needs. A scope missing here is a 403 from one tool weeks later.
	ts := auth.TokenSource(ctx, oc, refresh, cfg.HTTPTimeout)
	tok, err := ts.Token()
	if err != nil {
		report(false, "exchanging the refresh token", err.Error()+"\n       run `google-calendar-mcp login` again")
		_, _ = fmt.Fprintf(stdout, "\n%d problem(s).\n", problems)
		return nil
	}
	report(true, "access token", "")

	if info, err := auth.Inspect(ctx, nil, tok.AccessToken); err != nil {
		report(false, "inspecting the token", err.Error())
	} else {
		missing := auth.MissingScopes(info.Scopes, wanted)
		if len(missing) > 0 {
			report(false, "granted scopes",
				"not granted:\n       "+strings.Join(missing, "\n       ")+
					"\n       run `google-calendar-mcp login` again and accept every scope")
		} else {
			report(true, "granted scopes", fmt.Sprintf("all %d granted", len(wanted)))
		}
		if info.Email != "" {
			_, _ = fmt.Fprintf(stdout, "       account: %s\n", info.Email)
		}
	}

	// 4. The API answers. This is where "the Calendar API is not enabled
	//    in this Cloud project" shows up, and it has its own message
	//    because it is the single most common first-run failure.
	api := gapi.New(oauthClient(ctx, ts, cfg.HTTPTimeout))
	if _, err := api.ListSettings(ctx); err != nil {
		detail := err.Error()
		if cls, ok := gapi.ClassOf(err); ok && (cls == gapi.ClassForbidden || cls == gapi.ClassAuth) {
			detail += "\n       If this says the API is disabled, enable \"Google Calendar API\" in the " +
				"Cloud project that issued this OAuth client."
		}
		report(false, "Calendar API reachable", detail)
	} else {
		report(true, "Calendar API reachable", "")
	}

	// 5. A zone can be resolved at all. §4.1 refuses rather than falling
	//    back, so an account with no zone anywhere is a real problem and
	//    is better found here than on the first read.
	if settings, err := api.ListSettings(ctx); err == nil {
		if tz, ok := settings.Lookup("timezone"); ok && tz != "" {
			report(true, "account time zone", tz)
		} else {
			report(false, "account time zone",
				"this account's Calendar settings name no time zone; pass time_zone on calls, "+
					"or set one in Google Calendar")
		}
	}

	if problems == 0 {
		_, _ = fmt.Fprintln(stdout, "\nNo problems found.")
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "\n%d problem(s).\n", problems)
	return nil
}
