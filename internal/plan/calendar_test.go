package plan_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v2/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v2/internal/plan"
)

func TestCalendarPatchSendsOnlyWhatChanged(t *testing.T) {
	before := gcal.Calendar{
		ID: "c1@group.calendar.example.test", Summary: "Team", Description: "ours",
		TimeZone: "Europe/Copenhagen",
	}
	patch, changes, err := plan.CalendarDraft{
		Title:       ptr("Team"), // unchanged
		Description: ptr("ours, mostly"),
	}.Patch(before)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Summary != nil {
		t.Fatal("a field was sent that was already at that value; the etag would move for nothing")
	}
	if patch.Description == nil || *patch.Description != "ours, mostly" {
		t.Fatalf("description %v, want the new text", patch.Description)
	}
	if len(changes) != 1 || changes[0].Field != "description" {
		t.Fatalf("changes %+v, want only the description", changes)
	}
}

// Clearing a description is a real request, and a pointer to an empty
// string is how it is said. Emptying a TITLE is not: a calendar nobody
// can name is not something anybody means.
func TestCalendarPatchClearsAFieldButNotTheTitle(t *testing.T) {
	before := gcal.Calendar{Summary: "Team", Description: "ours", Location: "Copenhagen"}

	patch, changes, err := plan.CalendarDraft{Location: ptr("")}.Patch(before)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Location == nil || *patch.Location != "" {
		t.Fatalf("location %v, want an explicit empty string", patch.Location)
	}
	if len(changes) != 1 || changes[0].To != "" {
		t.Fatalf("changes %+v, want the location emptied", changes)
	}

	if _, _, err := (plan.CalendarDraft{Title: ptr("   ")}).Patch(before); !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("error %v, want invalid for an empty title", err)
	}
}

// §4.1: a zone that does not exist will not start existing, so it is
// refused here rather than sent and 400'd.
func TestCalendarPatchRefusesAZoneThatDoesNotExist(t *testing.T) {
	_, _, err := plan.CalendarDraft{TimeZone: ptr("Mars/Olympus")}.Patch(gcal.Calendar{})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("error %v, want invalid", err)
	}
	if _, _, err := (plan.CalendarDraft{TimeZone: ptr("Pacific/Auckland")}).Patch(gcal.Calendar{}); err != nil {
		t.Fatalf("a real zone was refused: %v", err)
	}
}

func TestListPatchIsPerUserAndComplete(t *testing.T) {
	before := gcal.CalendarListEntry{
		ID: "c1@group.calendar.example.test", Summary: "Team", ColorID: "3", Selected: true,
	}
	patch, changes, err := plan.ListDraft{
		MyName: ptr("The team thing"), ColorID: ptr("7"), Hidden: ptr(true), Selected: ptr(false),
	}.Patch(before)
	if err != nil {
		t.Fatal(err)
	}
	if patch.SummaryOverride == nil || patch.ColorID == nil || patch.Hidden == nil || patch.Selected == nil {
		t.Fatalf("patch %+v, want all four overrides", patch)
	}
	if len(changes) != 4 {
		t.Fatalf("changes %+v, want four", changes)
	}
	// hidden and selected are two different facts and both are reported
	// in words a person can read.
	for _, c := range changes {
		if c.Field == "hidden" && c.To != "yes" {
			t.Fatalf("hidden reported as %q", c.To)
		}
	}
}

// A color cannot be emptied: a calendar always has one, and an empty
// colorId would be a request Google refuses for a reason the caller
// cannot see from here.
func TestListPatchRefusesAnEmptyColor(t *testing.T) {
	_, _, err := plan.ListDraft{ColorID: ptr("")}.Patch(gcal.CalendarListEntry{ColorID: "3"})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("error %v, want invalid", err)
	}
}

// The notifications list is replaced whole, so the change line has to
// show both sides as sets rather than as one added type.
func TestListPatchReplacesTheWholeNotificationList(t *testing.T) {
	before := gcal.CalendarListEntry{
		NotificationSettings: &gcal.NotificationSettings{
			Notifications: []gcal.CalendarNotification{
				{Type: gcal.NotifyAgenda, Method: gcal.NotificationMethodEmail},
			},
		},
	}
	patch, changes, err := plan.ListDraft{
		Notifications: &[]string{"cancellation", "creation"},
	}.Patch(before)
	if err != nil {
		t.Fatal(err)
	}
	if patch.NotificationSettings == nil || len(patch.NotificationSettings.Notifications) != 2 {
		t.Fatalf("patch %+v, want two notifications", patch.NotificationSettings)
	}
	// In the published order, so two callers asking for the same set
	// produce the same request and the same change line.
	if patch.NotificationSettings.Notifications[0].Type != gcal.NotifyEventCreation {
		t.Fatalf("order %+v, want the published one", patch.NotificationSettings.Notifications)
	}
	for _, n := range patch.NotificationSettings.Notifications {
		if n.Method != gcal.NotificationMethodEmail {
			t.Fatalf("method %q, want email — the only one Google publishes", n.Method)
		}
	}
	if len(changes) != 1 || !strings.Contains(changes[0].From, gcal.NotifyAgenda) {
		t.Fatalf("changes %+v, want both sides of the list", changes)
	}
}

func TestListPatchTurnsNotificationsOff(t *testing.T) {
	before := gcal.CalendarListEntry{
		NotificationSettings: &gcal.NotificationSettings{
			Notifications: []gcal.CalendarNotification{{Type: gcal.NotifyAgenda}},
		},
	}
	patch, changes, err := plan.ListDraft{Notifications: &[]string{}}.Patch(before)
	if err != nil {
		t.Fatal(err)
	}
	if patch.NotificationSettings == nil {
		t.Fatal("an empty list sent nothing; Google would leave the notifications on")
	}
	if len(patch.NotificationSettings.Notifications) != 0 {
		t.Fatalf("notifications %+v, want none", patch.NotificationSettings.Notifications)
	}
	if len(changes) != 1 || changes[0].To != "none" {
		t.Fatalf("changes %+v, want the list emptied", changes)
	}
}

func TestNotificationTypesTakeBothSpellings(t *testing.T) {
	patch, _, err := plan.ListDraft{
		Notifications: &[]string{"eventChange", "response"},
	}.Patch(gcal.CalendarListEntry{})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, n := range patch.NotificationSettings.Notifications {
		got = append(got, n.Type)
	}
	if strings.Join(got, ",") != gcal.NotifyEventChange+","+gcal.NotifyEventResponse {
		t.Fatalf("types %v, want Google's spellings", got)
	}
}

func TestNotificationChoicesExplainThemselves(t *testing.T) {
	choices := plan.NotificationChoices()
	for _, want := range gcal.NotificationTypes {
		if !strings.Contains(choices, want) {
			t.Fatalf("the notification list omits %q", want)
		}
	}
	if !strings.Contains(choices, gcal.NotificationMeans(gcal.NotifyAgenda)) {
		t.Fatalf("the types are echoed rather than explained:\n%s", choices)
	}
}

// §9: the destructive tools take a confirmation on the call as well as
// the flag on the server, and the refusal says what would be destroyed.
func TestConfirmRefusesWithoutIt(t *testing.T) {
	err := plan.Confirm(false, "this deletes the calendar %q and every event on it")
	if !errors.Is(err, plan.ErrBlocked) {
		t.Fatalf("error %v, want blocked", err)
	}
	if !strings.Contains(err.Error(), "confirm:true") {
		t.Fatalf("the refusal does not say how to confirm: %v", err)
	}
	if err := plan.Confirm(true, "anything"); err != nil {
		t.Fatalf("confirm:true was still refused: %v", err)
	}
}
