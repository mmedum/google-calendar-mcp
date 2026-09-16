package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/internal/service"
)

// The three resources, for a client that attaches rather than calls
// (§8). They carry what the matching tool returns and nothing else: a
// resource that rendered a calendar differently from get_calendar would
// be a second description of the same thing, drifting from the first.
//
// Reads only, so read-only mode keeps all three and no gate removes
// them. A resource cannot take arguments, which is why there is no
// resource for a schedule: a window and a zone are not optional here
// (§4.5), and a URI with no way to state them would have to invent both.
const (
	uriCalendars = "gcal://calendars"
	// The + is RFC 6570's reserved expansion, and it is not decoration.
	// Under plain {calendar_id} a template matches only unreserved
	// characters, and every secondary calendar id is an address — so the
	// obvious URI, with the at sign written as itself, does not match and
	// comes back "not found" while the percent-encoded form works. With
	// + both forms match, and parseURI unescapes either into one id.
	tmplCalendar = "gcal://calendars/{+calendar_id}"
	tmplEvent    = "gcal://calendars/{+calendar_id}/events/{+event_id}"
)

const resourceMIME = "text/plain"

// registerResources adds the three resources.
func registerResources(s *mcp.Server, d Deps) {
	s.AddResource(&mcp.Resource{
		URI:      uriCalendars,
		Name:     "calendars",
		Title:    "Calendars",
		MIMEType: resourceMIME,
		Description: "Every calendar this account can see, with each one's id, time zone and access " +
			"level. The same content as the list_calendars tool.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		cals, err := d.Service.Calendars(ctx, false)
		if err != nil {
			return nil, err
		}
		return contents(req.Params.URI, service.NewCalendarsResult(cals).Render()), nil
	})

	// One handler for both templates, because the SDK routes a read to
	// the FIRST template that matches and reserved expansion makes the
	// calendar template match an event URI too. Which of the two a
	// client asked for is a fact about the URI, so the URI decides it
	// here rather than the order two registrations happen to be in.
	read := func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		calendar, event, err := parseURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		if event == "" {
			out, derr := d.Service.CalendarDetail(ctx, calendar)
			if derr != nil {
				return nil, derr
			}
			return contents(req.Params.URI, out.Render()), nil
		}
		e, zone, gerr := d.Service.GetEvent(ctx, calendar, event, "")
		if gerr != nil {
			return nil, gerr
		}
		return contents(req.Params.URI, service.NewEventResult(e, zone).Render()), nil
	}

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: tmplCalendar,
		Name:        "calendar",
		Title:       "One calendar",
		MIMEType:    resourceMIME,
		Description: "One calendar: its description, time zone, your access level and who else can see " +
			"it. The same content as the get_calendar tool.",
	}, read)

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: tmplEvent,
		Name:        "event",
		Title:       "One event",
		MIMEType:    resourceMIME,
		Description: "One event on one calendar, in the calendar's own time zone. The same content as " +
			"the get_event tool.",
	}, read)
}

// contents wraps rendered text as the one content block a read returns.
func contents(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: uri, MIMEType: resourceMIME, Text: text,
	}}}
}

// parseURI pulls the calendar and event ids out of a gcal:// URI.
//
// It parses rather than trusting the SDK's own template match, because
// the ids end up in a request path: a URI that matched a template and
// carried something else in the segment would be passed straight to
// Google. Each segment is unescaped once, so a client that encoded the
// @ in a calendar id and one that did not name the same calendar.
func parseURI(uri string) (calendar, event string, err error) {
	rest, ok := strings.CutPrefix(uri, uriCalendars+"/")
	if !ok || rest == "" {
		return "", "", mcp.ResourceNotFoundError(uri)
	}
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		calendar = parts[0]
	case 3:
		// An empty event segment is not a calendar read. Falling through
		// with event == "" answered gcal://calendars/primary/events/
		// with the CALENDAR card, sharing list and all — a different
		// resource from the one the URI names, with no error.
		if parts[1] != "events" || parts[2] == "" {
			return "", "", mcp.ResourceNotFoundError(uri)
		}
		calendar, event = parts[0], parts[2]
	default:
		return "", "", mcp.ResourceNotFoundError(uri)
	}

	if calendar, err = url.PathUnescape(calendar); err != nil {
		return "", "", fmt.Errorf("%s is not a readable URI: %w", uri, err)
	}
	if event, err = url.PathUnescape(event); err != nil {
		return "", "", fmt.Errorf("%s is not a readable URI: %w", uri, err)
	}
	if strings.TrimSpace(calendar) == "" {
		return "", "", mcp.ResourceNotFoundError(uri)
	}
	return calendar, event, nil
}
