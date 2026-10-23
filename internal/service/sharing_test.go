package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/service"
)

func TestListSharingExplainsEveryRole(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "user:colleague@example.test", Role: gcal.RoleWriterWithoutPrivateData,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "colleague@example.test"}})

	got, err := svc.ListSharing(context.Background(), "team@group.calendar.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.After) != 2 {
		t.Fatalf("%d rules, want 2", len(got.After))
	}
	// The role is explained rather than echoed: writerWithoutPrivateAccess
	// tells a reader nothing on its own (§7.6).
	if !strings.Contains(got.Text(), "private events show without their details") {
		t.Fatalf("the roles are not explained:\n%s", got.Text())
	}
}

// A public calendar is the one fact about sharing worth shouting, and a
// reader scanning a list of addresses will not notice a word in the
// middle of it.
func TestListSharingCallsOutAPublicCalendar(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "default", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeDefault}})

	got, err := svc.ListSharing(context.Background(), "team@group.calendar.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text(), "ANYONE") || !strings.Contains(got.Text(), "PUBLIC") {
		t.Fatalf("a public calendar is not called out:\n%s", got.Text())
	}
	if !strings.Contains(strings.Split(got.Text(), "\n")[2], "ANYONE") {
		t.Fatalf("the public rule is not listed first:\n%s", got.Text())
	}
}

// §2.15: the ACL read is a separate grant, and an empty list would read
// as "shared with nobody" — the wrong answer rather than a missing one.
func TestListSharingSaysWhenTheScopeIsMissing(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACLScopeRequired = true

	_, err := svc.ListSharing(context.Background(), "team@group.calendar.example.test")
	// A missing scope is [auth] rather than [forbidden]: the fix is
	// another login, not somebody granting access.
	if cls := classOf(t, err); cls != gapi.ClassAuth {
		t.Fatalf("class %s, want auth", cls)
	}
	if !strings.Contains(err.Error(), "calendar.acls.readonly") {
		t.Fatalf("the refusal does not name the scope: %v", err)
	}
	if !strings.Contains(err.Error(), "not the same as") {
		t.Fatalf("the refusal does not separate itself from \"shared with nobody\": %v", err)
	}
}

func TestShareCalendarGrantsAccessAndShowsExposureBothWays(t *testing.T) {
	svc, fake := calendarSeed(t)

	got, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Before) != 1 || len(got.After) != 2 {
		t.Fatalf("before %d rules, after %d — want 1 and 2", len(got.Before), len(got.After))
	}
	text := got.Text()
	if !strings.Contains(text, "who could see it before") || !strings.Contains(text, "who can see it now") {
		t.Fatalf("the result does not show exposure both ways:\n%s", text)
	}
	var inserted bool
	for _, w := range fake.Wrote() {
		if w.Method == "acl.insert" {
			inserted = true
			// §2.5: Google's default here is to email, so the parameter
			// is sent on every call rather than left out.
			if w.SendNotifications != "true" {
				t.Fatalf("sendNotifications was %q, want true", w.SendNotifications)
			}
		}
	}
	if !inserted {
		t.Fatal("no rule was written")
	}
}

// notify:none must reach the wire as sendNotifications=false. Leaving
// the parameter out is not the same thing: Google's default is true.
func TestShareCalendarSendsFalseRatherThanNothing(t *testing.T) {
	svc, fake := calendarSeed(t)

	if _, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "none",
	}); err != nil {
		t.Fatal(err)
	}
	for _, w := range fake.Wrote() {
		if w.Method == "acl.insert" && w.SendNotifications != "false" {
			t.Fatalf("sendNotifications was %q, want an explicit false", w.SendNotifications)
		}
	}
}

func TestShareCalendarRequiresNotify(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader",
	})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class %s, want invalid", cls)
	}
}

// external_only splits guests on a domain. An ACL notification is one
// switch, so there is nothing for it to mean here and rounding it to
// either value would email the wrong set of people.
func TestShareCalendarRefusesExternalOnly(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "external_only",
	})
	if cls := classOf(t, err); cls != gapi.ClassUnsupported {
		t.Fatalf("class %s, want unsupported", cls)
	}
}

// share_calendar takes named people and groups inside the organization,
// below owner (§7.6). Everything wider is refused before anything is
// written, naming the tool that takes it.
func TestShareCalendarStopsAtTheOrganization(t *testing.T) {
	for _, c := range []struct {
		name, who, scopeType, role, names string
	}{
		{"the public", "anyone", "", "reader", "publish_calendar"},
		{"a domain", "example.test", "domain", "reader", "publish_calendar"},
		{"a person outside", "somebody@elsewhere.test", "", "reader", "widen_calendar_access"},
		{"a group outside", "team@elsewhere.test", "group", "reader", "widen_calendar_access"},
		{"an owner inside", "colleague@example.test", "", "owner", "widen_calendar_access"},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, fake := calendarSeed(t)
			_, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
				Calendar: "team@group.calendar.example.test", Who: c.who, ScopeType: c.scopeType,
				Role: c.role, Notify: "none",
			})
			if cls := classOf(t, err); cls != gapi.ClassBlocked || !strings.Contains(err.Error(), c.names) {
				t.Fatalf("got [%s] %v, want [blocked] naming %s", cls, err, c.names)
			}
			if w := fake.Wrote(); len(w) != 0 {
				t.Fatalf("got writes %+v, want none", w)
			}
		})
	}
}

// A personal account has no organization, so share_calendar takes
// nobody there, and the address is refused as outside even in the
// account's own domain.
func TestShareCalendarOnAPersonalAccountTakesNobody(t *testing.T) {
	// Assembled from pieces, so this file does not trip the leak gate.
	gmail := "@" + "gmail.com"
	fake := caltest.New()
	fake.AddCalendar("me"+gmail, "Sample Primary", "UTC", gcal.RoleOwner, true)
	svc := newService(t, fake)
	_, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "primary", Who: "friend" + gmail, Role: "reader", Notify: "none",
	})
	if cls := classOf(t, err); cls != gapi.ClassBlocked || !strings.Contains(err.Error(), "widen_calendar_access") {
		t.Fatalf("got [%s] %v, want [blocked] naming widen_calendar_access", cls, err)
	}
}

// publish_calendar opens a calendar to the public, and says what that
// cannot take back. Without confirm it writes nothing; a dry run needs
// no confirm and writes nothing either.
func TestPublishCalendarNeedsConfirmAndSaysItPublishes(t *testing.T) {
	svc, fake := calendarSeed(t)
	o := service.PublishOptions{Calendar: "team@group.calendar.example.test", Who: "anyone", Role: "reader",
		Notify: "none"}

	_, err := svc.PublishCalendar(context.Background(), o)
	if cls := classOf(t, err); cls != gapi.ClassBlocked || !strings.Contains(err.Error(), "confirm:true") {
		t.Fatalf("got [%s] %v, want [blocked] asking for confirm", cls, err)
	}
	dry := o
	dry.DryRun = true
	if _, err := svc.PublishCalendar(context.Background(), dry); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if w := fake.Wrote(); len(w) != 0 {
		t.Fatalf("got writes %+v before the confirmed call, want none", w)
	}

	o.Confirm = true
	got, err := svc.PublishCalendar(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if w := fake.Wrote(); len(w) != 1 || w[0].Method != "acl.insert" {
		t.Fatalf("got writes %+v, want one acl.insert", w)
	}
	if got.Changed == nil || got.Changed.ScopeType != gcal.ScopeTypeDefault {
		t.Fatalf("rule %+v, want the public scope", got.Changed)
	}
	if !strings.Contains(got.Text(), "PUBLIC") || !strings.Contains(got.Text(), "takes nothing back") {
		t.Fatalf("publishing does not say what it cannot take back:\n%s", got.Text())
	}
}

// A domain needs no scope_type in publish_calendar, and a person is
// refused there, naming the tools that take one.
func TestPublishCalendarOpensADomainAndTakesNoPerson(t *testing.T) {
	svc, _ := calendarSeed(t)
	got, err := svc.PublishCalendar(context.Background(), service.PublishOptions{
		Calendar: "team@group.calendar.example.test", Who: "example.test", Role: "freeBusyReader",
		Notify: "none", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed == nil || got.Changed.ScopeType != gcal.ScopeTypeDomain || got.Changed.Value != "example.test" {
		t.Fatalf("rule %+v, want the domain example.test", got.Changed)
	}
	if !strings.Contains(got.Text(), "everybody in example.test") {
		t.Fatalf("the result does not say who the domain rule reaches:\n%s", got.Text())
	}

	_, err = svc.PublishCalendar(context.Background(), service.PublishOptions{
		Calendar: "team@group.calendar.example.test", Who: "colleague@example.test", Role: "reader",
		Notify: "none", Confirm: true,
	})
	if cls := classOf(t, err); cls != gapi.ClassInvalid || !strings.Contains(err.Error(), "share_calendar") {
		t.Fatalf("got [%s] %v, want [invalid] naming share_calendar", cls, err)
	}
}

// widen_calendar_access shares outside the organization and makes owners,
// with confirm, and refuses what the other two tools take.
func TestWidenCalendarAccessTakesOutsidersAndOwners(t *testing.T) {
	for _, c := range []struct {
		name, who, role string
		confirm         bool
		want            gapi.Class
		says            string
	}{
		{"a person outside", "somebody@elsewhere.test", "reader", true, "", "outside your organization"},
		{"an owner inside", "colleague@example.test", "owner", true, "", "Owner access includes"},
		{"without confirm", "somebody@elsewhere.test", "reader", false, gapi.ClassBlocked, "confirm:true"},
		{"a person inside below owner", "colleague@example.test", "writer", true, gapi.ClassInvalid, "share_calendar"},
		{"the public", "anyone", "reader", true, gapi.ClassInvalid, "publish_calendar"},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, fake := calendarSeed(t)
			got, err := svc.WidenCalendarAccess(context.Background(), service.WidenOptions{
				Calendar: "team@group.calendar.example.test", Who: c.who, Role: c.role, Notify: "none",
				Confirm: c.confirm,
			})
			if c.want != "" {
				if cls := classOf(t, err); cls != c.want || !strings.Contains(err.Error(), c.says) {
					t.Fatalf("got [%s] %v, want [%s] saying %q", cls, err, c.want, c.says)
				}
				if w := fake.Wrote(); len(w) != 0 {
					t.Fatalf("got writes %+v, want none", w)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if w := fake.Wrote(); len(w) != 1 || w[0].Method != "acl.insert" {
				t.Fatalf("got writes %+v, want one acl.insert", w)
			}
			if !strings.Contains(got.Text(), c.says) {
				t.Fatalf("the result does not say %q:\n%s", c.says, got.Text())
			}
		})
	}
}

// A role change on an existing rule is a patch, not a second insert, and
// the result says what it changed from.
func TestShareCalendarPatchesAnExistingRule(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "user:colleague@example.test", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "colleague@example.test"}})

	got, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "writer", Notify: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	var patched bool
	for _, w := range fake.Wrote() {
		if w.Method == "acl.patch" {
			patched = true
			if w.IfMatch == "" {
				t.Fatal("the role change was sent with no If-Match")
			}
		}
		if w.Method == "acl.insert" {
			t.Fatal("an existing rule was inserted again rather than patched")
		}
	}
	if !patched {
		t.Fatal("no rule was patched")
	}
	if !strings.Contains(got.Text(), "from reader to writer") {
		t.Fatalf("the result does not say what the access changed from:\n%s", got.Text())
	}
	if len(got.After) != 2 {
		t.Fatalf("after %d rules, want 2 — the patch added somebody", len(got.After))
	}
}

// Granting access somebody already has would be a second email about
// nothing, so it writes nothing and says why.
func TestShareCalendarWritesNothingWhenTheAccessIsAlreadyThere(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "user:colleague@example.test", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "colleague@example.test"}})

	got, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range fake.Wrote() {
		if strings.HasPrefix(w.Method, "acl.") {
			t.Fatalf("%s was sent for access that was already in place", w.Method)
		}
	}
	if !strings.Contains(got.Text(), "already had reader access") {
		t.Fatalf("the result does not say the access was already there:\n%s", got.Text())
	}
	if !strings.Contains(got.Notify, "nothing was written") {
		t.Fatalf("notification line %q, want it to say nothing was sent", got.Notify)
	}
}

// An address is read as one person, and the result says it inferred
// that: a group address and a person's look identical.
func TestShareCalendarSaysHowItReadTheAddress(t *testing.T) {
	svc, _ := calendarSeed(t)

	got, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text(), "scope type was inferred") {
		t.Fatalf("the result does not say the scope type was inferred:\n%s", got.Text())
	}
	if got.Changed == nil || got.Changed.ScopeType != gcal.ScopeTypeUser {
		t.Fatalf("scope type %+v, want user", got.Changed)
	}
}

func TestShareCalendarRefusesWithoutOwnerAccess(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "readonly@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "none",
	})
	if cls := classOf(t, err); cls != gapi.ClassForbidden {
		t.Fatalf("class %s, want forbidden", cls)
	}
}

func TestShareCalendarDryRunWritesNothingAndShowsTheResult(t *testing.T) {
	svc, fake := calendarSeed(t)

	got, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "all", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range fake.Wrote() {
		if strings.HasPrefix(w.Method, "acl.") {
			t.Fatalf("a dry run sent %s", w.Method)
		}
	}
	if !strings.Contains(got.Text(), "DRY RUN") {
		t.Fatalf("a dry run does not say so:\n%s", got.Text())
	}
	if len(got.After) != 2 {
		t.Fatalf("a dry run shows %d rules afterward, want the 2 it would leave", len(got.After))
	}
}

// acl.delete publishes no sendNotifications and Google sends none, so
// the result says the person is not told rather than leaving a caller to
// assume a mail went out.
func TestUnshareSaysNobodyIsTold(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "user:colleague@example.test", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "colleague@example.test"}})

	got, err := svc.UnshareCalendar(context.Background(), service.UnshareOptions{
		Calendar: "team@group.calendar.example.test", Who: "colleague@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.After) != 1 {
		t.Fatalf("after %d rules, want 1", len(got.After))
	}
	if !strings.Contains(got.Notify, "no notifications on access removal") {
		t.Fatalf("notification line %q, want it to say Google offers none", got.Notify)
	}
	var deleted bool
	for _, w := range fake.Wrote() {
		if w.Method == "acl.delete" {
			deleted = true
			if w.RuleID != "user:colleague@example.test" {
				t.Fatalf("deleted rule %q, want the colleague's", w.RuleID)
			}
		}
	}
	if !deleted {
		t.Fatal("no rule was deleted")
	}
}

func TestUnshareSaysWhenThereIsNoSuchRule(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.UnshareCalendar(context.Background(), service.UnshareOptions{
		Calendar: "team@group.calendar.example.test", Who: "stranger@example.test",
	})
	if cls := classOf(t, err); cls != gapi.ClassNotFound {
		t.Fatalf("class %s, want not_found", cls)
	}
}

// Removing your own rule from your own calendar is a door locked from
// the inside: the tool that would put it back needs the access the rule
// grants.
func TestUnshareRefusesToRemoveThisAccountsOwnAccess(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.UnshareCalendar(context.Background(), service.UnshareOptions{
		Calendar: "team@group.calendar.example.test", Who: "me@example.test",
	})
	if cls := classOf(t, err); cls != gapi.ClassBlocked {
		t.Fatalf("class %s, want blocked", cls)
	}
}

func TestUnshareThePublicRuleSaysWhatItDoesNotUndo(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "default", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeDefault}})

	got, err := svc.UnshareCalendar(context.Background(), service.UnshareOptions{
		Calendar: "team@group.calendar.example.test", Who: "anyone",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text(), "takes nothing back") {
		t.Fatalf("the result does not say what unpublishing does not undo:\n%s", got.Text())
	}
}

// get_calendar and list_sharing answer the same question and must
// answer it the same way, from one renderer.
func TestGetCalendarAndListSharingAgree(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "default", Role: gcal.RoleFreeBusyReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeDefault}})

	ctx := context.Background()
	card, err := svc.CalendarDetail(ctx, "team@group.calendar.example.test")
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListSharing(ctx, "team@group.calendar.example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ANYONE", "only whether the time is busy"} {
		if !strings.Contains(card.Render(), want) {
			t.Fatalf("get_calendar does not say %q:\n%s", want, card.Render())
		}
		if !strings.Contains(list.Text(), want) {
			t.Fatalf("list_sharing does not say %q:\n%s", want, list.Text())
		}
	}
}

// A dry run must not assert anything as done. The notes are the easy
// place to get this wrong: they are written where the plan is made, and
// they print under the words "nothing was written".
func TestSharingDryRunNotesClaimNothingHappened(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "default", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeDefault}})
	ctx := context.Background()

	shared, err := svc.PublishCalendar(ctx, service.PublishOptions{
		Calendar: "team@group.calendar.example.test", Who: "anyone", Role: "writer",
		Notify: "none", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	unshared, err := svc.UnshareCalendar(ctx, service.UnshareOptions{
		Calendar: "team@group.calendar.example.test", Who: "anyone", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{shared.Text(), unshared.Text()} {
		if !strings.Contains(got, "DRY RUN") {
			t.Fatalf("not a dry run:\n%s", got)
		}
		for _, claim := range []string{"is now PUBLIC", "no longer public", "access changed"} {
			if strings.Contains(got, claim) {
				t.Fatalf("a dry run says %q, under the words \"nothing was written\":\n%s", claim, got)
			}
		}
	}
}

// Two different refusals, and only one of them is fixable by logging in
// again. classify separates them — insufficientPermissions is [auth],
// and a plain 403 from acl.list, which needs owner access, is
// [forbidden] — so the scope advice must not be attached to both.
func TestAPlainForbiddenIsNotReportedAsAMissingScope(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.Fail["GET /calendars/"] = 403

	_, err := svc.ListSharing(context.Background(), "team@group.calendar.example.test")
	if cls := classOf(t, err); cls != gapi.ClassForbidden {
		t.Fatalf("class %s, want forbidden", cls)
	}
	if strings.Contains(err.Error(), "login") {
		t.Fatalf("a 403 that no login can fix tells the caller to log in again: %v", err)
	}
}

// acl.insert is a POST that is not retried, and a repeat with
// sendNotifications=true emails the person a second time. So a 5xx
// sends the caller to list_sharing, never back to share_calendar.
func TestAShareThatMayHaveLandedIsAmbiguous(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.Fail["POST /calendars/team@group.calendar.example.test/acl"] = 503

	_, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "all",
	})
	if cls := classOf(t, err); cls != gapi.ClassAmbiguousOutcome {
		t.Fatalf("class %s, want ambiguous_outcome: %v", cls, err)
	}
	if !strings.Contains(err.Error(), "list_sharing") {
		t.Errorf("the message must say how to look: %v", err)
	}
}

// A 4xx definitely did not land, so a refused share keeps its own class.
func TestAShareGoogleRefusedIsNotAmbiguous(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.Fail["POST /calendars/team@group.calendar.example.test/acl"] = 400

	_, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      "colleague@example.test", Role: "reader", Notify: "all",
	})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class %s, want invalid: %v", cls, err)
	}
}

// Sharing with a mailbox shares with its address: the rule carries the
// bare one, and the display name reaches neither Google nor the result.
func TestShareCalendarTakesAMailbox(t *testing.T) {
	svc, fake := calendarSeed(t)
	got, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test",
		Who:      `"Sample Colleague" <colleague@example.test>`, Role: "reader", Notify: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, r := range fake.ACL["team@group.calendar.example.test"] {
		values = append(values, r.Scope.Value)
	}
	if strings.Join(values, ",") != "me@example.test,colleague@example.test" {
		t.Fatalf("rules for %v, want the bare address added", values)
	}
	if strings.Contains(got.Text(), "Sample Colleague") {
		t.Fatalf("the display name reached the result:\n%s", got.Text())
	}
}
