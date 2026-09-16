package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/service"
)

// calendarSeed is a small account: the user's own calendar, a team
// calendar they own, and one they can only read.
func calendarSeed(t *testing.T) (*service.Service, *caltest.Server) {
	t.Helper()
	const tz = "Europe/Copenhagen"
	fake := caltest.New()
	fake.AddCalendar("me@example.test", "Sample Primary", tz, gcal.RoleOwner, true)
	fake.AddCalendar("team@group.calendar.example.test", "Sample Team", "America/Chicago", gcal.RoleOwner, false)
	fake.AddCalendar("readonly@group.calendar.example.test", "Sample Readonly", "UTC", gcal.RoleReader, false)
	fake.ACL["team@group.calendar.example.test"] = []gcal.AclRule{
		{ID: "user:me@example.test", Role: gcal.RoleOwner,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "me@example.test"}},
	}
	fake.Settings = []gcal.Setting{{ID: gcal.SettingTimezone, Value: tz}}
	return newService(t, fake), fake
}

func TestCreateCalendarSendsTheResolvedZone(t *testing.T) {
	svc, _ := calendarSeed(t)

	got, err := svc.CreateCalendar(context.Background(), service.CreateCalendarOptions{
		Title: "Project Kestrel", TimeZone: "America/Chicago",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Calendar.TimeZone != "America/Chicago" {
		t.Fatalf("created in %q, want the zone that was asked for", got.Calendar.TimeZone)
	}
	if got.Calendar.ID == "" {
		t.Fatal("the new calendar has no id; nothing else can address it")
	}
	if !strings.Contains(got.Text(), "America/Chicago") {
		t.Fatalf("the result does not name the zone it used:\n%s", got.Text())
	}
}

// §4.1: a calendar created with no zone must not take the process's.
// With none on the call and no calendar to read, the account's own
// setting is the answer, and the result says so.
func TestCreateCalendarFallsBackToTheAccountZone(t *testing.T) {
	svc, _ := calendarSeed(t)

	got, err := svc.CreateCalendar(context.Background(), service.CreateCalendarOptions{Title: "Reading"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Calendar.TimeZone != "Europe/Copenhagen" {
		t.Fatalf("created in %q, want the account's own zone", got.Calendar.TimeZone)
	}
	if !strings.Contains(got.Text(), "settings") {
		t.Fatalf("the result does not say where the zone came from:\n%s", got.Text())
	}
}

func TestCreateCalendarNeedsATitle(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.CreateCalendar(context.Background(), service.CreateCalendarOptions{Title: "  "})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class %s, want invalid", cls)
	}
}

// A dry run writes nothing, and says what it cannot know: Google mints
// the id, so there is none to report.
func TestCreateCalendarDryRunWritesNothing(t *testing.T) {
	svc, fake := calendarSeed(t)

	got, err := svc.CreateCalendar(context.Background(), service.CreateCalendarOptions{
		Title: "Project Kestrel", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text(), "DRY RUN") || !strings.Contains(got.Said(), "Would") {
		t.Fatalf("a dry run does not say so:\n%s", got.Text())
	}
	for _, w := range fake.Wrote() {
		if w.Method == "calendars.insert" {
			t.Fatal("a dry run created a calendar")
		}
	}
}

// §7.5's distinction, held at the API: a title change goes to
// calendars.patch and a colour to calendarList.patch, and neither
// touches the other resource.
func TestManageSeparatesTheCalendarFromTheSubscription(t *testing.T) {
	svc, fake := calendarSeed(t)
	title, colour := "Team — renamed", "7"

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Title: &title, ColorID: &colour,
	})
	if err != nil {
		t.Fatal(err)
	}
	var patchedCalendar, patchedEntry bool
	for _, w := range fake.Wrote() {
		switch w.Method {
		case "calendars.patch":
			patchedCalendar = true
		case "calendarList.patch":
			patchedEntry = true
		}
	}
	if !patchedCalendar || !patchedEntry {
		t.Fatalf("calendar patched: %v, subscription patched: %v — both were asked for",
			patchedCalendar, patchedEntry)
	}
	if len(got.Changes) != 2 {
		t.Fatalf("changes %+v, want the title and the colour", got.Changes)
	}
	if !strings.Contains(got.Text(), "everybody") {
		t.Fatalf("a rename of the calendar itself does not say it is visible to everybody:\n%s", got.Text())
	}
}

// my_name is summaryOverride: it renames the calendar for this account
// and for nobody else, so it must never reach calendars.patch.
func TestManageMyNameNeverTouchesTheSharedCalendar(t *testing.T) {
	svc, fake := calendarSeed(t)
	mine := "The team thing"

	if _, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", MyName: &mine,
	}); err != nil {
		t.Fatal(err)
	}
	for _, w := range fake.Wrote() {
		if w.Method == "calendars.patch" {
			t.Fatal("renaming it for yourself renamed it for everybody")
		}
	}
	if fake.Calendars["team@group.calendar.example.test"].Summary != "Sample Team" {
		t.Fatal("the calendar's own title changed")
	}
}

// A calendar this account can only read still takes the per-user
// settings: the colour and the name you give it are yours, whatever
// your access to the calendar is.
func TestManageOverridesWorkOnAReadOnlyCalendar(t *testing.T) {
	svc, _ := calendarSeed(t)
	hidden := true

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "readonly@group.calendar.example.test", Hidden: &hidden,
	})
	if err != nil {
		t.Fatalf("hiding a read-only calendar was refused: %v", err)
	}
	if len(got.Changes) != 1 || got.Changes[0].Field != "hidden" {
		t.Fatalf("changes %+v, want hidden", got.Changes)
	}
}

// ...while changing the calendar itself needs access to it, and the
// refusal says what access is missing rather than letting Google answer
// 403.
func TestManageRefusesTheSharedHalfWithoutAccess(t *testing.T) {
	svc, _ := calendarSeed(t)
	title := "Not mine to rename"

	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "readonly@group.calendar.example.test", Title: &title,
	})
	if cls := classOf(t, err); cls != gapi.ClassForbidden {
		t.Fatalf("class %s, want forbidden", cls)
	}
}

func TestManageRefusesACallThatAsksForNothing(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{Calendar: "primary"})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class %s, want invalid", cls)
	}
}

// Unsubscribing removes the list entry and nothing else. The test
// asserts the calendar and its events survive, because that is the
// sentence the tool description promises.
func TestUnsubscribeLeavesTheCalendarAndItsEvents(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.AddEvent("team@group.calendar.example.test",
		caltest.Timed("evteam00001", "Team planning",
			"2026-03-16T15:00:00+01:00", "2026-03-16T16:00:00+01:00", "America/Chicago"))

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Unsubscribe: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, still := fake.Entries["team@group.calendar.example.test"]; still {
		t.Fatal("the subscription is still there")
	}
	if _, gone := fake.Calendars["team@group.calendar.example.test"]; !gone {
		t.Fatal("unsubscribing deleted the calendar")
	}
	if len(fake.Events["team@group.calendar.example.test"]) != 1 {
		t.Fatal("unsubscribing removed the events")
	}
	if !strings.Contains(got.Text(), "not deleted") && !strings.Contains(got.Text(), "untouched") {
		t.Fatalf("the result does not say the calendar survives:\n%s", got.Text())
	}
}

func TestUnsubscribeRefusesThePrimaryCalendar(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "primary", Unsubscribe: true,
	})
	if cls := classOf(t, err); cls != gapi.ClassBlocked {
		t.Fatalf("class %s, want blocked", cls)
	}
}

// Unsubscribing and changing something in the same call cannot both be
// meant: the entry the other change lands on is the one being removed.
func TestUnsubscribeRefusesToCarryOtherChanges(t *testing.T) {
	svc, _ := calendarSeed(t)
	colour := "5"
	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Unsubscribe: true, ColorID: &colour,
	})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class %s, want invalid", cls)
	}
}

func TestSubscribeAddsACalendarReachedByID(t *testing.T) {
	svc, fake := calendarSeed(t)
	// A calendar that exists and that this account is not subscribed to,
	// which is the case subscribing is for.
	fake.Calendars["other@group.calendar.example.test"] = &gcal.Calendar{
		ID: "other@group.calendar.example.test", Summary: "Somebody else's", TimeZone: "UTC",
	}

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "other@group.calendar.example.test", Subscribe: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.Entries["other@group.calendar.example.test"]; !ok {
		t.Fatal("the calendar was not added to the list")
	}
	if !strings.Contains(got.Text(), "grants no access") {
		t.Fatalf("the result does not say subscribing grants nothing:\n%s", got.Text())
	}
}

// Subscribing to a calendar already in the list writes nothing and says
// so, rather than sending a request whose answer nobody can predict.
func TestSubscribeToSomethingAlreadySubscribedWritesNothing(t *testing.T) {
	svc, fake := calendarSeed(t)

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Subscribe: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range fake.Wrote() {
		if w.Method == "calendarList.insert" {
			t.Fatal("it subscribed again to something already in the list")
		}
	}
	if !strings.Contains(got.Text(), "Already in your calendar list") {
		t.Fatalf("the result does not say it was already there:\n%s", got.Text())
	}
}

// A time zone that does not exist is [invalid] and local: it will not
// start existing, so telling the caller to retry would be wrong, and
// spending a request to be told so would be waste.
func TestManageRefusesAnUnknownTimeZone(t *testing.T) {
	svc, fake := calendarSeed(t)
	tz := "Mars/Olympus"

	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", TimeZone: &tz,
	})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class %s, want invalid", cls)
	}
	for _, w := range fake.Wrote() {
		if w.Method == "calendars.patch" {
			t.Fatal("an impossible zone was sent to Google anyway")
		}
	}
}

// §4.4 at the calendar: the patch carries the etag of the read that
// produced it, and the two resources carry different etags — so the
// entry's must never end up on the calendar's write.
func TestCalendarPatchesCarryTheirOwnETag(t *testing.T) {
	svc, fake := calendarSeed(t)
	title, colour := "Renamed", "3"

	if _, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Title: &title, ColorID: &colour,
	}); err != nil {
		t.Fatal(err)
	}
	for _, w := range fake.Wrote() {
		switch w.Method {
		case "calendars.patch", "calendarList.patch":
			if w.IfMatch == "" {
				t.Fatalf("%s was sent with no If-Match", w.Method)
			}
			if w.IfMatch == "*" {
				t.Fatalf("%s forced the write through; nothing asked for that", w.Method)
			}
		}
	}
}

// A patch that would change nothing is not sent: it would move the etag
// under everybody else holding one, for no change.
func TestManageSendsNothingWhenTheValuesAlreadyMatch(t *testing.T) {
	svc, fake := calendarSeed(t)
	same := "Sample Team"

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Title: &same,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range fake.Wrote() {
		if w.Method == "calendars.patch" {
			t.Fatal("a patch was sent for a value that was already set")
		}
	}
	if len(got.Changes) != 0 {
		t.Fatalf("changes %+v, want none", got.Changes)
	}
}

// The notifications list is replaced whole, and an empty list turns them
// all off — which is what Google does with the object.
func TestManageNotificationsReplaceTheWholeList(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.Entries["team@group.calendar.example.test"].NotificationSettings = &gcal.NotificationSettings{
		Notifications: []gcal.CalendarNotification{
			{Type: gcal.NotifyEventCreation, Method: gcal.NotificationMethodEmail},
			{Type: gcal.NotifyAgenda, Method: gcal.NotificationMethodEmail},
		},
	}
	want := []string{"cancellation"}

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Notifications: &want,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored := fake.Entries["team@group.calendar.example.test"].NotificationSettings
	if stored == nil || len(stored.Notifications) != 1 ||
		stored.Notifications[0].Type != gcal.NotifyEventCancellation {
		t.Fatalf("stored notifications %+v, want only the cancellation one", stored)
	}
	if stored.Notifications[0].Method != gcal.NotificationMethodEmail {
		t.Fatalf("method %q, want email — the only one Google publishes",
			stored.Notifications[0].Method)
	}
	if len(got.Changes) != 1 || got.Changes[0].Field != "notifications" {
		t.Fatalf("changes %+v, want the notifications", got.Changes)
	}

	none := []string{}
	if _, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Notifications: &none,
	}); err != nil {
		t.Fatal(err)
	}
	if stored := fake.Entries["team@group.calendar.example.test"].NotificationSettings; stored == nil ||
		len(stored.Notifications) != 0 {
		t.Fatalf("an empty list left %+v, want none", stored)
	}
}

func TestManageRefusesAnUnknownNotificationType(t *testing.T) {
	svc, _ := calendarSeed(t)
	want := []string{"whenever anything happens"}
	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Notifications: &want,
	})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class %s, want invalid", cls)
	}
}

// A write that changed the list must not leave the process answering
// from the list it had before. The cache is what keeps a resolution from
// costing a request, and a stale one answers a later call wrongly.
func TestARenameInvalidatesTheCalendarCache(t *testing.T) {
	svc, _ := calendarSeed(t)
	ctx := context.Background()
	if _, err := svc.Calendars(ctx, true); err != nil {
		t.Fatal(err)
	}
	title := "Sample Team renamed"
	if _, err := svc.ManageCalendar(ctx, service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Title: &title,
	}); err != nil {
		t.Fatal(err)
	}
	cals, err := svc.Calendars(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cals {
		if c.ID == "team@group.calendar.example.test" && c.Title != title {
			t.Fatalf("the list still reports %q after the rename", c.Title)
		}
	}
}

// ------------------------------------------------------- destructive

func TestDeleteCalendarNeedsConfirm(t *testing.T) {
	svc, fake := calendarSeed(t)
	_, err := svc.DeleteCalendar(context.Background(), service.DestructiveOptions{
		Calendar: "team@group.calendar.example.test",
	})
	if cls := classOf(t, err); cls != gapi.ClassBlocked {
		t.Fatalf("class %s, want blocked", cls)
	}
	if _, gone := fake.Calendars["team@group.calendar.example.test"]; !gone {
		t.Fatal("the calendar was deleted without confirmation")
	}
}

func TestDeleteCalendarRemovesItWithConfirm(t *testing.T) {
	svc, fake := calendarSeed(t)
	got, err := svc.DeleteCalendar(context.Background(), service.DestructiveOptions{
		Calendar: "team@group.calendar.example.test", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, still := fake.Calendars["team@group.calendar.example.test"]; still {
		t.Fatal("the calendar is still there")
	}
	if !strings.Contains(got.Text(), "cannot bring it back") {
		t.Fatalf("the result does not say it is irreversible:\n%s", got.Text())
	}
}

// Google's delete removes a SECONDARY calendar. The primary is refused
// here, with the sentence that says what to do instead.
func TestDeleteCalendarRefusesThePrimary(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.DeleteCalendar(context.Background(), service.DestructiveOptions{
		Calendar: "primary", Confirm: true,
	})
	if cls := classOf(t, err); cls != gapi.ClassUnsupported {
		t.Fatalf("class %s, want unsupported", cls)
	}
	if !strings.Contains(err.Error(), "clear_calendar") {
		t.Fatalf("the refusal does not name the tool that empties it: %v", err)
	}
}

func TestDeleteCalendarRefusesWhatThisAccountDoesNotOwn(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.DeleteCalendar(context.Background(), service.DestructiveOptions{
		Calendar: "readonly@group.calendar.example.test", Confirm: true,
	})
	if cls := classOf(t, err); cls != gapi.ClassForbidden {
		t.Fatalf("class %s, want forbidden", cls)
	}
}

func TestDeleteCalendarDryRunKeepsIt(t *testing.T) {
	svc, fake := calendarSeed(t)
	got, err := svc.DeleteCalendar(context.Background(), service.DestructiveOptions{
		Calendar: "team@group.calendar.example.test", Confirm: true, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, still := fake.Calendars["team@group.calendar.example.test"]; !still {
		t.Fatal("a dry run deleted the calendar")
	}
	if !strings.Contains(got.Said(), "Would") {
		t.Fatalf("a dry run reported %q", got.Said())
	}
}

// clear empties the account's own primary calendar and nothing else,
// which is what Google documents the method as doing.
func TestClearCalendarEmptiesThePrimary(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.AddEvent("me@example.test", caltest.Timed("evclear0001", "Something",
		"2026-03-16T09:00:00+01:00", "2026-03-16T09:30:00+01:00", "Europe/Copenhagen"))

	got, err := svc.ClearCalendar(context.Background(), service.DestructiveOptions{
		Calendar: "primary", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Events["me@example.test"]) != 0 {
		t.Fatalf("%d events left after a clear", len(fake.Events["me@example.test"]))
	}
	if _, still := fake.Calendars["me@example.test"]; !still {
		t.Fatal("clear deleted the calendar itself")
	}
	if !strings.Contains(got.Text(), "not notified") {
		t.Fatalf("the result does not say guests are not told:\n%s", got.Text())
	}
}

func TestClearCalendarNeedsConfirm(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.AddEvent("me@example.test", caltest.Timed("evclear0002", "Something",
		"2026-03-16T09:00:00+01:00", "2026-03-16T09:30:00+01:00", "Europe/Copenhagen"))

	_, err := svc.ClearCalendar(context.Background(), service.DestructiveOptions{Calendar: "primary"})
	if cls := classOf(t, err); cls != gapi.ClassBlocked {
		t.Fatalf("class %s, want blocked", cls)
	}
	if len(fake.Events["me@example.test"]) == 0 {
		t.Fatal("the calendar was cleared without confirmation")
	}
}

// Google documents clear as clearing a PRIMARY calendar, so a secondary
// one is refused rather than sent and hoped for.
func TestClearCalendarRefusesASecondaryCalendar(t *testing.T) {
	svc, _ := calendarSeed(t)
	_, err := svc.ClearCalendar(context.Background(), service.DestructiveOptions{
		Calendar: "team@group.calendar.example.test", Confirm: true,
	})
	if cls := classOf(t, err); cls != gapi.ClassUnsupported {
		t.Fatalf("class %s, want unsupported", cls)
	}
	if !strings.Contains(err.Error(), "delete_calendar") {
		t.Fatalf("the refusal does not say what to use instead: %v", err)
	}
}

// A calendar is two resources with two etags, and each write has to
// carry the right one. Sending the subscription's to calendars.delete is
// a 412 on every call, and it reads to the caller as somebody else's
// edit — so the fixture gives the two different values and this asserts
// which one went where.
//
// The two halves run against separate fixtures on purpose. The first
// draft did both in one, unsubscribing before deleting, and passed
// against the defect it was written to catch: with the subscription
// gone, the calendar could only be resolved through calendars.get, so
// the ambiguous field happened to hold the right etag. A test whose
// setup removes the confusion it is testing for proves nothing.
func TestEachWriteCarriesItsOwnResourcesETag(t *testing.T) {
	const id = "team@group.calendar.example.test"

	t.Run("unsubscribe carries the subscription's", func(t *testing.T) {
		svc, fake := calendarSeed(t)
		want := etagsApart(t, fake, id)[1]
		if _, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
			Calendar: id, Unsubscribe: true,
		}); err != nil {
			t.Fatal(err)
		}
		assertIfMatch(t, fake, "calendarList.delete", want)
	})

	t.Run("delete carries the calendar's", func(t *testing.T) {
		svc, fake := calendarSeed(t)
		want := etagsApart(t, fake, id)[0]
		if _, err := svc.DeleteCalendar(context.Background(), service.DestructiveOptions{
			Calendar: id, Confirm: true,
		}); err != nil {
			t.Fatal(err)
		}
		assertIfMatch(t, fake, "calendars.delete", want)
	})

	t.Run("clear carries the calendar's", func(t *testing.T) {
		svc, fake := calendarSeed(t)
		want := etagsApart(t, fake, "me@example.test")[0]
		if _, err := svc.ClearCalendar(context.Background(), service.DestructiveOptions{
			Calendar: "primary", Confirm: true,
		}); err != nil {
			t.Fatal(err)
		}
		assertIfMatch(t, fake, "calendars.clear", want)
	})
}

// etagsApart returns the calendar's etag and its subscription's, having
// first established that they are not the same value — without which
// none of the assertions above can fail.
func etagsApart(t *testing.T, fake *caltest.Server, id string) [2]string {
	t.Helper()
	calendar, entry := fake.Calendars[id].ETag, fake.Entries[id].ETag
	if calendar == "" || entry == "" || calendar == entry {
		t.Fatalf("the fixture cannot tell the two etags apart: %q and %q", calendar, entry)
	}
	return [2]string{calendar, entry}
}

func assertIfMatch(t *testing.T, fake *caltest.Server, method, want string) {
	t.Helper()
	for _, w := range fake.Wrote() {
		if w.Method != method {
			continue
		}
		if w.IfMatch != want {
			t.Fatalf("%s carried If-Match %q, want %q — that is the other resource's etag",
				method, w.IfMatch, want)
		}
		return
	}
	t.Fatalf("%s was never sent", method)
}

// Renaming a calendar you had renamed FOR YOURSELF must leave your own
// name alone: summaryOverride is a different field from the calendar's
// title, and a result that showed the new shared title as "the name you
// gave it" would contradict itself in two consecutive lines.
func TestRenamingASharedCalendarKeepsYourOwnNameForIt(t *testing.T) {
	const id = "team@group.calendar.example.test"
	svc, fake := calendarSeed(t)
	fake.Entries[id].SummaryOverride = "The team thing"
	title := "Team planning"

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: id, Title: &title,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Calendar.Title != "The team thing" {
		t.Fatalf("the result calls it %q; the name this account gave it is unchanged and should win",
			got.Calendar.Title)
	}
	if got.Calendar.Original != title {
		t.Fatalf("the result says others see %q, want the new title %q", got.Calendar.Original, title)
	}
	if strings.Contains(got.Text(), "others see \"The team thing\"") {
		t.Fatalf("the result contradicts itself about which name is whose:\n%s", got.Text())
	}
}

// §4.7: one tool call is one API request, and where it cannot be, the
// result says how many it made. Phase 1 shipped a defect where one call
// spent 168 requests and reported 2 — invisible, because the number in
// the result was kept by hand. These assert the reported count against
// what the fake actually served, and pin the counts themselves so a
// second read added later fails here rather than in somebody's quota.
func TestTheCalendarWritesSpendWhatTheySay(t *testing.T) {
	colour, title := "5", "Renamed"

	for _, c := range []struct {
		name  string
		spend int
		call  func(*service.Service) (int, error)
	}{
		{name: "manage_calendar, my own view", spend: 3, call: func(svc *service.Service) (int, error) {
			got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
				Calendar: "primary", ColorID: &colour,
			})
			return got.Requests, err
		}},
		{name: "manage_calendar, the calendar itself", spend: 3, call: func(svc *service.Service) (int, error) {
			got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
				Calendar: "team@group.calendar.example.test", Title: &title,
			})
			return got.Requests, err
		}},
		{name: "list_sharing", spend: 2, call: func(svc *service.Service) (int, error) {
			got, err := svc.ListSharing(context.Background(), "team@group.calendar.example.test")
			return got.Requests, err
		}},
		{name: "share_calendar", spend: 3, call: func(svc *service.Service) (int, error) {
			got, err := svc.ShareCalendar(context.Background(), service.ShareOptions{
				Calendar: "team@group.calendar.example.test",
				Who:      "colleague@example.test", Role: "reader", Notify: "none",
			})
			return got.Requests, err
		}},
		{name: "delete_calendar", spend: 3, call: func(svc *service.Service) (int, error) {
			got, err := svc.DeleteCalendar(context.Background(), service.DestructiveOptions{
				Calendar: "team@group.calendar.example.test", Confirm: true,
			})
			return got.Requests, err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, fake := calendarSeed(t)
			reported, err := c.call(svc)
			if err != nil {
				t.Fatal(err)
			}
			served := len(fake.Served())
			if reported != served {
				t.Fatalf("reported %d API requests, served %d — the count a caller reads is not the "+
					"one Google was asked for", reported, served)
			}
			if served != c.spend {
				t.Fatalf("spent %d requests, expected %d. If the change is deliberate, say why here and "+
					"in the result", served, c.spend)
			}
		})
	}
}

// The cached calendar list is corrected by a write rather than dropped,
// so a second call does not pay for a whole list read — and a calendar
// this process created can be found by the name it was given.
func TestAWriteKeepsTheCalendarListUsable(t *testing.T) {
	svc, fake := calendarSeed(t)
	ctx := context.Background()
	colour := "5"

	if _, err := svc.ManageCalendar(ctx, service.ManageOptions{
		Calendar: "team@group.calendar.example.test", ColorID: &colour,
	}); err != nil {
		t.Fatal(err)
	}
	before := len(fake.Served())
	second := "7"
	if _, err := svc.ManageCalendar(ctx, service.ManageOptions{
		Calendar: "team@group.calendar.example.test", ColorID: &second,
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range fake.Served()[before:] {
		if path == "GET /users/me/calendarList" {
			t.Fatal("the second call re-read the whole calendar list; the first should have corrected it")
		}
	}

	// And the created-calendar case, which was a dead end: the caller
	// gave the calendar a name and could not then address it by that
	// name for the life of the process.
	made, err := svc.CreateCalendar(ctx, service.CreateCalendarOptions{Title: "Project Kestrel"})
	if err != nil {
		t.Fatal(err)
	}
	found, err := svc.ResolveCalendar(ctx, "Project Kestrel")
	if err != nil {
		t.Fatalf("the calendar this call just created cannot be found by its title: %v", err)
	}
	if found.ID != made.Calendar.ID {
		t.Fatalf("resolved %q, want the calendar just created", found.ID)
	}
}

// manage_calendar can be two patches, and the second failing does not
// undo the first. There is no rollback to offer, so the refusal has to
// name what already stands — otherwise the caller retries and redoes it.
func TestAPartialManageCallSaysWhatAlreadyLanded(t *testing.T) {
	const id = "team@group.calendar.example.test"
	svc, fake := calendarSeed(t)
	fake.Fail["PATCH /users/me/calendarList/"] = 500
	title, colour := "Renamed", "5"

	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: id, Title: &title, ColorID: &colour,
	})
	if err == nil {
		t.Fatal("the failing half reported success")
	}
	if !strings.Contains(err.Error(), "title") {
		t.Fatalf("the refusal does not name the change that landed: %v", err)
	}
	if !strings.Contains(err.Error(), "no rollback") {
		t.Fatalf("the refusal does not say the change stands: %v", err)
	}
	if fake.Calendars[id].Summary != title {
		t.Fatal("the test is not exercising a partial write: the first patch did not land")
	}
}

// "Subscribe to this calendar and set my colour on it" is what the tool
// description offers, and a dry run of it has to work: the entry does
// not exist yet, so reading it 404s and the refusal tells the caller to
// pass subscribe:true — which they did.
func TestADryRunCanSubscribeAndSetYourOwnView(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.Calendars["other@group.calendar.example.test"] = &gcal.Calendar{
		ID: "other@group.calendar.example.test", Summary: "Somebody else's", TimeZone: "UTC",
	}
	colour := "7"

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "other@group.calendar.example.test", Subscribe: true, ColorID: &colour, DryRun: true,
	})
	if err != nil {
		t.Fatalf("a dry run of subscribe plus a colour was refused: %v", err)
	}
	if len(got.Changes) != 1 || got.Changes[0].Field != "color_id" {
		t.Fatalf("changes %+v, want the colour it would set", got.Changes)
	}
	for _, w := range fake.Wrote() {
		t.Fatalf("a dry run wrote %s", w.Method)
	}
}

// A dry run that fails half way through wrote nothing, so it must not
// report anything as standing.
func TestAFailedDryRunClaimsNothingLanded(t *testing.T) {
	const id = "team@group.calendar.example.test"
	svc, fake := calendarSeed(t)
	fake.Fail["GET /users/me/calendarList/"] = 500
	title, colour := "Renamed", "5"

	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: id, Title: &title, ColorID: &colour, DryRun: true,
	})
	if err == nil {
		t.Fatal("the failing read reported success")
	}
	if strings.Contains(err.Error(), "that change stands") {
		t.Fatalf("a dry run says a change stands:\n%v", err)
	}
}

// Both halves of a dry run describe the same plan: the calendar block
// cannot show the old title above a change list saying it changed.
func TestADryRunShowsBothHalvesOfWhatItWouldDo(t *testing.T) {
	const id = "team@group.calendar.example.test"
	svc, _ := calendarSeed(t)
	title, colour := "Renamed Team", "7"

	got, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: id, Title: &title, ColorID: &colour, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Calendar.Title != title {
		t.Fatalf("the calendar shown is titled %q while the change list says it becomes %q:\n%s",
			got.Calendar.Title, title, got.Text())
	}
	if got.Calendar.ColorID != colour {
		t.Fatalf("colour shown is %q, want the one it would set", got.Calendar.ColorID)
	}
}

// Google refuses to let a calendar's data owner remove it from their own
// list — 403, "The data owner of a calendar cannot remove such a
// calendar from their calendar list" — which the live run found and
// nothing published says. The refusal is translated into the two things
// that do work, rather than passed through as somebody else's sentence.
func TestUnsubscribingFromYourOwnCalendarSaysWhatDoesWork(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.Fail["DELETE /users/me/calendarList/"] = 403
	fake.FailMessage["DELETE /users/me/calendarList/"] =
		"The data owner of a calendar cannot remove such a calendar from their calendar list."

	_, err := svc.ManageCalendar(context.Background(), service.ManageOptions{
		Calendar: "team@group.calendar.example.test", Unsubscribe: true,
	})
	if cls := classOf(t, err); cls != gapi.ClassUnsupported {
		t.Fatalf("class %s, want unsupported", cls)
	}
	for _, want := range []string{"hidden:true", "delete_calendar"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not offer %q: %v", want, err)
		}
	}
}
