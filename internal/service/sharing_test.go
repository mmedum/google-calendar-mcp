package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/service"
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

// §7.6: the public scope needs an explicit flag, because removing the
// rule afterwards takes nothing back.
func TestShareCalendarRefusesThePublicScopeWithoutTheFlag(t *testing.T) {
	svc, fake := calendarSeed(t)

	_, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test", Who: "anyone", Role: "reader", Notify: "none",
	})
	if cls := classOf(t, err); cls != gapi.ClassBlocked {
		t.Fatalf("class %s, want blocked", cls)
	}
	for _, w := range fake.Wrote() {
		if w.Method == "acl.insert" {
			t.Fatal("the calendar was published anyway")
		}
	}
}

func TestShareCalendarPublishesWithTheFlagAndSaysSo(t *testing.T) {
	svc, _ := calendarSeed(t)

	got, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
		Calendar: "team@group.calendar.example.test", Who: "anyone", Role: "reader",
		Notify: "none", AllowPublic: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text(), "PUBLIC") {
		t.Fatalf("publishing a calendar does not say so:\n%s", got.Text())
	}
	if !strings.Contains(got.Text(), "takes nothing back") {
		t.Fatalf("the result does not say the rule cannot be undone from the other side:\n%s", got.Text())
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
		t.Fatalf("a dry run shows %d rules afterwards, want the 2 it would leave", len(got.After))
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
