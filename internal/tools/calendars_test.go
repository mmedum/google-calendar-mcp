package tools_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/tools"
)

// §7.6: GCAL_SHARING=off removes the sharing surface — the read among
// them, because a deployment that turned sharing off turned off the
// surface and not merely the writes, and the wider shares even with the
// destructive flag set.
func TestSharingOffRemovesEverySharingTool(t *testing.T) {
	cfg := tools.FullSurface(baseConfig())
	cfg.Sharing = false
	got := names(listTools(t, cfg))

	for _, name := range append(append([]string{}, sharingTools...), wideSharingTools...) {
		if _, ok := got[name]; ok {
			t.Fatalf("GCAL_SHARING=off still registered %q", name)
		}
	}
	// And it removes nothing else: the calendar tools are a different
	// axis and must survive.
	for _, name := range append(append([]string{}, readTools...), calendarTools...) {
		if _, ok := got[name]; !ok {
			t.Fatalf("GCAL_SHARING=off removed %q, which is not a sharing tool", name)
		}
	}
}

// §9: the two gated tools do not exist unless the flag is set, because
// an annotation is a hint a host in auto-approve may ignore.
func TestTheGatedToolsNeedTheFlag(t *testing.T) {
	got := names(listTools(t, baseConfig()))
	for _, name := range gatedTools {
		if _, ok := got[name]; ok {
			t.Fatalf("%q registered without GCAL_ENABLE_DESTRUCTIVE", name)
		}
	}

	cfg := baseConfig()
	cfg.EnableDestructive = true
	armed := names(listTools(t, cfg))
	for _, name := range gatedTools {
		tool, ok := armed[name]
		if !ok {
			t.Fatalf("%q did not register with the flag set", name)
		}
		if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
			t.Fatalf("%q is not annotated destructive", name)
		}
		if !strings.Contains(tool.Description, "confirm") {
			t.Fatalf("%q does not say it needs confirm on the call:\n%s", name, tool.Description)
		}
	}
}

// The mark goes only where §3 puts it (§9a): the delete of a whole
// calendar, and of every event on the primary one, neither of which
// Calendar can bring back, and a public or domain-wide share. Claude
// Code then prompts on every such call. It is listed to every client:
// each one here declares elicitation, and the mark used to be dropped
// for such a client.
func TestOnlyTheWholeCalendarDeletesPromptEveryCall(t *testing.T) {
	var got []string
	for _, tool := range listTools(t, tools.FullSurface(baseConfig())) {
		if tool.Meta["anthropic/requiresUserInteraction"] == true {
			got = append(got, tool.Name)
		}
	}
	slices.Sort(got)
	if want := []string{"clear_calendar", "delete_calendar", "publish_calendar"}; !slices.Equal(got, want) {
		t.Errorf("tools carrying requiresUserInteraction = %v, want %v", got, want)
	}
}

// list_sharing reads and changes nothing, and its annotation says so
// even though the sharing gate removes it.
func TestListSharingIsAnnotatedReadOnly(t *testing.T) {
	tool := names(listTools(t, tools.FullSurface(baseConfig())))["list_sharing"]
	if tool == nil {
		t.Fatal("list_sharing is missing from the full surface")
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Fatal("list_sharing reads and is not annotated read-only")
	}
}

// Sharing is the one act whose effect leaves this account, and the
// annotation is the only place a client learns that before calling.
func TestTheSharingWritesClaimAnOpenWorld(t *testing.T) {
	got := names(listTools(t, tools.FullSurface(baseConfig())))
	for _, name := range []string{"share_calendar", "unshare_calendar", "publish_calendar", "widen_calendar_access"} {
		a := got[name].Annotations
		if a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Fatalf("%q changes who outside this account can see a calendar and does not say so", name)
		}
	}
	// A calendar write does not reach outside the account.
	for _, name := range calendarTools {
		a := got[name].Annotations
		if a.OpenWorldHint != nil && *a.OpenWorldHint {
			t.Fatalf("%q claims an open world and does not have one", name)
		}
	}
}

// The distinctions a model gets wrong are the ones the descriptions have
// to carry: which of the two resources a change lands on, what
// unsubscribing does not do, and that sharing notification has its own
// default.
func TestTheCalendarToolsTeachTheirOwnTraps(t *testing.T) {
	got := names(listTools(t, tools.FullSurface(baseConfig())))

	manage := got["manage_calendar"].Description
	for _, want := range []string{"everybody", "my_name", "not delete", "notifications"} {
		if !strings.Contains(manage, want) {
			t.Fatalf("manage_calendar does not explain %q:\n%s", want, manage)
		}
	}
	share := got["share_calendar"].Description
	for _, want := range []string{"notify", "publish_calendar", "widen_calendar_access", "scope_type", "dry_run"} {
		if !strings.Contains(share, want) {
			t.Fatalf("share_calendar does not explain %q:\n%s", want, share)
		}
	}
	unshare := got["unshare_calendar"].Description
	if !strings.Contains(unshare, "no notify") && !strings.Contains(unshare, "not told") {
		t.Fatalf("unshare_calendar does not say nobody is told:\n%s", unshare)
	}
	if strings.Contains(got["clear_calendar"].Description, "delete_calendar") == false {
		t.Fatalf("clear_calendar does not point at the tool for a secondary calendar:\n%s",
			got["clear_calendar"].Description)
	}
}

// signedIn is a calendar set whose primary calendar has the account's
// address for its id, as Google's does, so a share can tell an address
// inside the organization from one outside it. Seed's is the literal
// "primary", which leaves every address outside.
func signedIn() *caltest.Server {
	const tz = "Europe/Copenhagen"
	fake := caltest.New()
	fake.AddCalendar("owner@example.test", "Sample Primary", tz, gcal.RoleOwner, true)
	fake.AddCalendar("team@group.calendar.example.test", "Sample Team", "America/Chicago", gcal.RoleWriter, false)
	fake.ACL["owner@example.test"] = []gcal.AclRule{
		{ID: "user:owner@example.test", Role: gcal.RoleOwner,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "owner@example.test"}},
		{ID: "user:colleague@example.test", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "colleague@example.test"}},
	}
	fake.Settings = []gcal.Setting{{ID: gcal.SettingTimezone, Value: tz}}
	return fake
}

// End to end through the protocol: the gated tools refuse without
// confirm, each share is taken by the tool its reach belongs to, and a
// refusal arrives as a classified tool result rather than a protocol
// error.
func TestTheCalendarToolsAnswerThroughTheProtocol(t *testing.T) {
	cs, cleanup := sessionWith(t, tools.FullSurface(baseConfig()), signedIn())
	defer cleanup()
	ctx := context.Background()

	calls := []struct {
		name    string
		args    map[string]any
		want    string
		isError bool
	}{
		{name: "create_calendar", args: map[string]any{
			"title": "Project Kestrel", "dry_run": true,
		}, want: "Would create"},
		{name: "list_sharing", args: map[string]any{"calendar": "primary"}, want: "Who can see"},
		{name: "share_calendar", args: map[string]any{
			"calendar": "primary", "who": "somebody@example.test", "role": "reader",
			"notify": "none", "dry_run": true,
		}, want: "who can see it now"},
		{name: "share_calendar", args: map[string]any{
			"calendar": "primary", "who": "somebody@example.test", "role": "reader",
		}, want: "[invalid]", isError: true},
		{name: "share_calendar", args: map[string]any{
			"calendar": "primary", "who": "anyone", "role": "reader", "notify": "none",
		}, want: "publish_calendar", isError: true},
		{name: "share_calendar", args: map[string]any{
			"calendar": "primary", "who": "somebody@elsewhere.test", "role": "reader", "notify": "none",
		}, want: "widen_calendar_access", isError: true},
		{name: "publish_calendar", args: map[string]any{
			"calendar": "primary", "who": "anyone", "role": "freeBusyReader", "notify": "none",
		}, want: "[blocked]", isError: true},
		{name: "publish_calendar", args: map[string]any{
			"calendar": "primary", "who": "anyone", "role": "freeBusyReader", "notify": "none", "dry_run": true,
		}, want: "PUBLIC"},
		{name: "widen_calendar_access", args: map[string]any{
			"calendar": "primary", "who": "somebody@elsewhere.test", "role": "reader", "notify": "none",
			"dry_run": true,
		}, want: "outside your organization"},
		{name: "unshare_calendar", args: map[string]any{
			"calendar": "primary", "who": "colleague@example.test", "dry_run": true,
		}, want: "no notification"},
		{name: "manage_calendar", args: map[string]any{
			"calendar": "primary", "hidden": true, "dry_run": true,
		}, want: "Would update"},
		// Access is checked before the confirmation, which is the right
		// order: "pass confirm" is useless advice to somebody who could
		// not have done it anyway.
		{name: "delete_calendar", args: map[string]any{
			"calendar": "team@group.calendar.example.test",
		}, want: "[forbidden]", isError: true},
		{name: "clear_calendar", args: map[string]any{
			"calendar": "team@group.calendar.example.test", "confirm": true,
		}, want: "[unsupported]", isError: true},
	}
	for _, c := range calls {
		t.Run(c.name+"/"+c.want, func(t *testing.T) {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
			if err != nil {
				t.Fatalf("CallTool(%s): %v", c.name, err)
			}
			if res.IsError != c.isError {
				t.Fatalf("%s: isError=%v, want %v:\n%s", c.name, res.IsError, c.isError, text(t, res))
			}
			if !c.isError && res.StructuredContent == nil {
				t.Fatalf("%s returned no structured half", c.name)
			}
			if got := text(t, res); !strings.Contains(got, c.want) {
				t.Fatalf("%s did not mention %q:\n%s", c.name, c.want, got)
			}
		})
	}
}

// The two halves of §9's protection, end to end: the flag registers the
// tool, and the call still has to confirm. Driven against a calendar
// this account made in the same session, because deleting needs owner
// access and the seed's other calendars are not owned.
func TestDeleteCalendarThroughTheProtocolStillNeedsConfirm(t *testing.T) {
	cs, cleanup := session(t, tools.FullSurface(baseConfig()))
	defer cleanup()
	ctx := context.Background()

	made, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_calendar", Arguments: map[string]any{"title": "Scratch"},
	})
	if err != nil || made.IsError {
		t.Fatalf("create_calendar: %v %s", err, text(t, made))
	}
	id, _ := made.StructuredContent.(map[string]any)["calendar"].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("the new calendar reported no id:\n%s", text(t, made))
	}

	refused, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "delete_calendar", Arguments: map[string]any{"calendar": id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !refused.IsError || !strings.Contains(text(t, refused), "[blocked]") {
		t.Fatalf("deleting without confirm was allowed:\n%s", text(t, refused))
	}
	if !strings.Contains(text(t, refused), "every event on it") {
		t.Fatalf("the refusal does not say what would be destroyed:\n%s", text(t, refused))
	}

	done, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "delete_calendar", Arguments: map[string]any{"calendar": id, "confirm": true},
	})
	if err != nil || done.IsError {
		t.Fatalf("delete_calendar with confirm: %v %s", err, text(t, done))
	}
	if !strings.Contains(text(t, done), "Deleted") {
		t.Fatalf("the result does not say it was deleted:\n%s", text(t, done))
	}
}
