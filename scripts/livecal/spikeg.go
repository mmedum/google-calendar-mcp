//go:build live

package main

import (
	"context"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/auth"
	"github.com/mmedum/google-calendar-mcp/internal/redact"
)

// spikeG answers §15's question: does acl.list really need its own
// scope, or is calendar.readonly enough?
//
// The discovery document says acl.get lists calendar.readonly among its
// scopes and acl.list does not (§2.15). That is an odd enough asymmetry
// to be worth a live probe, because if it is wrong this server asks
// every user for a scope it does not need, and if it is right and we had
// not noticed, get_calendar would 403 weeks after a working setup.
//
// The spike adapts to whichever grant the profile actually has, so it
// produces evidence either way rather than only under one configuration:
//
//   - grant INCLUDES calendar.acls.readonly -> acl.list must succeed
//   - grant EXCLUDES it                     -> acl.list must fail
//
// Running it under a profile with GCAL_SHARING=off is what tests the
// second half, and that is the half the scope decision rests on.
func spikeG(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	// Ask the TOKEN what it carries, not the profile what was recorded.
	//
	// Google's grant is stored per OAuth client and user, and a later
	// authorization asking for fewer scopes does not revoke the ones
	// already given — so a profile that requested no ACL scope can still
	// hold a token that has one. Reading the recorded list would make
	// this spike answer confidently and wrongly.
	live, err := api.liveScopes(ctx)
	if err != nil {
		return undetermined, "could not read the token's actual scopes: " + err.Error()
	}
	if len(live) == 0 {
		return undetermined, "the token reported no scopes, so there is nothing to compare against"
	}
	granted := map[string]bool{}
	for _, s := range live {
		granted[s] = true
	}
	hasACLScope := granted[auth.ScopeACLReadonly] || granted[auth.ScopeACL]

	out.Printf("      grant includes calendar.acls.readonly: %v\n", hasACLScope)

	err = api.listACL(ctx, scratch)

	switch {
	case hasACLScope && err == nil:
		return pass, "acl.list works with the acls scope granted (the positive half; " +
			"the negative half needs a profile with GCAL_SHARING=off)"
	case hasACLScope && err != nil:
		return fail, "acl.list failed even though the acls scope was granted: " + err.Error()
	case !hasACLScope && err == nil:
		// This would refute §2.15 and mean the extra scope is
		// unnecessary — a real finding, and the one that would let this
		// server ask for less.
		return fail, "REFUTES §2.15: acl.list succeeded WITHOUT calendar.acls.readonly. " +
			"The extra scope is not needed; §10 and docs/gcp-setup.md should drop it"
	default:
		if isForbidden(err) {
			return pass, "CONFIRMS §2.15: acl.list is refused without calendar.acls.readonly"
		}
		return undetermined, "acl.list failed without the scope, but not with a 403: " + err.Error()
	}
}

func isForbidden(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "returned 403") || strings.Contains(s, "returned 401")
}
