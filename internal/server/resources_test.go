package server_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v2/internal/server"
)

// session connects a client to a real server over the in-memory
// transports, which is how a resource has to be read: the SDK does the
// template matching, and a test that called the handler directly would
// not be testing the thing a client does.
func session(t *testing.T, d server.Deps) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := server.New(d)
	ct, st := newTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Wait() })
	cs, err := newClient().Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func readResource(t *testing.T, cs *mcp.ClientSession, uri string) string {
	t.Helper()
	out, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("ReadResource(%s): %v", uri, err)
	}
	if len(out.Contents) != 1 {
		t.Fatalf("ReadResource(%s) returned %d contents, want 1", uri, len(out.Contents))
	}
	return out.Contents[0].Text
}

func TestResourcesAreListedWithTheirTemplates(t *testing.T) {
	cs := session(t, deps(t))
	ctx := context.Background()

	res, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 1 || res.Resources[0].URI != "gcal://calendars" {
		t.Fatalf("got resources %+v, want the calendar list", res.Resources)
	}
	tmpl, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.ResourceTemplates) != 2 {
		t.Fatalf("got %d templates, want the calendar and the event", len(tmpl.ResourceTemplates))
	}
}

// TestAResourceCarriesTheToolsOwnText is §8's rule: the resources are
// the tools' content for a client that attaches rather than calls. Two
// renderings of one calendar is how they come to disagree.
func TestAResourceCarriesTheToolsOwnText(t *testing.T) {
	d := deps(t)
	cs := session(t, d)

	list := readResource(t, cs, "gcal://calendars")
	out, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_calendars", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	text, ok := out.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("list_calendars returned %T, want text", out.Content[0])
	}
	if list != text.Text {
		t.Fatalf("the resource and the tool disagree.\nresource:\n%s\ntool:\n%s", list, text.Text)
	}
}

func TestACalendarAndAnEventRead(t *testing.T) {
	cs := session(t, deps(t))

	card := readResource(t, cs, "gcal://calendars/primary")
	if !strings.Contains(card, "primary") {
		t.Fatalf("the calendar resource does not name the calendar:\n%s", card)
	}

	// The seeded event, read through the template.
	events := readResource(t, cs, "gcal://calendars/primary/events/ev-standup")
	if !strings.Contains(events, "id: ev-standup") {
		t.Fatalf("the event resource is not that event:\n%s", events)
	}
}

// TestACalendarIDWithAnAtSignReadsEitherWay: a client that
// percent-encoded the @ and one that did not must name the same
// calendar, because both are legal in a path segment.
func TestACalendarIDWithAnAtSignReadsEitherWay(t *testing.T) {
	cs := session(t, deps(t))
	plain := readResource(t, cs, "gcal://calendars/team@group.calendar.example.test")
	encoded := readResource(t, cs, "gcal://calendars/team%40group.calendar.example.test")
	if plain != encoded {
		t.Fatalf("the encoded and plain forms disagree.\nplain:\n%s\nencoded:\n%s", plain, encoded)
	}
}

// TestAURIThatNamesNothingIsNotFound: a shape the templates do not
// describe must be refused rather than passed to Google as a calendar
// id.
func TestAURIThatNamesNothingIsNotFound(t *testing.T) {
	cs := session(t, deps(t))
	for _, uri := range []string{
		"gcal://calendars/primary/events",
		// An empty event segment is not the calendar: answering this
		// with the calendar card would be a different resource from the
		// one the URI names, with no error.
		"gcal://calendars/primary/events/",
		"gcal://calendars/primary/agenda/x",
		"gcal://calendars/",
		"gcal://something-else",
	} {
		if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Fatalf("ReadResource(%s) succeeded", uri)
		}
	}
}
