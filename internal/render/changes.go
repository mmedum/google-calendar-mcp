package render

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// Changes is what `list_changes` answers with (§17.1).
type Changes struct {
	CalendarID   string
	CalendarName string
	Zone         when.Zone

	// Changed are events that still exist; Deleted are the ids of ones
	// that do not. They are two lists rather than one with a flag
	// because a deleted event arrives bare — an id and a status, with no
	// title and no times — so there is nothing to print on a row beside
	// the others.
	Changed []model.Event
	Deleted []string

	// Baseline is a first call, with no token: everything on the
	// calendar comes back and nothing has "changed" yet.
	Baseline bool
	// Complete says the last page was reached, which is the only state
	// in which SyncToken is set.
	Complete      bool
	SyncToken     string
	NextPageToken string
	Requests      int
	// Skipped counts rows a baseline paged past without reporting. They
	// are not changes — the token covers them — and carrying hundreds of
	// tombstones to establish a starting point would be cost with no
	// answer in it.
	Skipped int
}

// Text renders what changed.
func (c Changes) Text() string {
	var b strings.Builder

	name := c.CalendarName
	if name == "" {
		name = c.CalendarID
	}
	if c.Baseline {
		fmt.Fprintf(&b, "Baseline for %s\n", name)
	} else {
		fmt.Fprintf(&b, "Changes on %s\n", name)
	}
	fmt.Fprintf(&b, "%s\n\n", c.Zone.Explain())

	switch {
	case c.Baseline:
		fmt.Fprintf(&b, "%d event%s on the calendar now. Nothing is reported as changed: this call "+
			"establishes the starting point.\n", len(c.Changed), plural(len(c.Changed)))
		if c.Skipped > 0 {
			fmt.Fprintf(&b, "%d further row%s were paged past to reach the sync token, which is what a "+
				"baseline is for. They are covered by the token, not lost.\n", c.Skipped, plural(c.Skipped))
		}
	case len(c.Changed) == 0 && len(c.Deleted) == 0:
		b.WriteString("Nothing changed.\n")
	}

	if len(c.Changed) > 0 {
		if !c.Baseline {
			fmt.Fprintf(&b, "Changed (%d)\n", len(c.Changed))
		}
		for _, e := range c.Changed {
			fmt.Fprintf(&b, "  %s\n", InstanceLine(e, c.Zone))
		}
		b.WriteString("\n")
	}
	if len(c.Deleted) > 0 {
		// The ids alone, deliberately. A deleted event's title is gone
		// from Google's answer, and inventing one from a cached read
		// would be this server claiming to know something it does not.
		//
		// The heading differs on a baseline, because the same rows mean
		// two different things. `showDeleted` is true on every call in a
		// sync series — Google forbids false alongside a token, and the
		// discovery document asks for the other parameters to match the
		// initial read — so a baseline picks up whatever is ALREADY
		// cancelled on the calendar. Those were not deleted since
		// anything; there was no "since" yet.
		if c.Baseline {
			fmt.Fprintf(&b, "Already cancelled (%d)\n", len(c.Deleted))
		} else {
			fmt.Fprintf(&b, "Deleted (%d)\n", len(c.Deleted))
		}
		for _, id := range c.Deleted {
			fmt.Fprintf(&b, "  %s\n", id)
		}
		b.WriteString("\n")
	}

	// The token, and the honest statement of when there isn't one.
	switch {
	case c.SyncToken != "":
		fmt.Fprintf(&b, "Next sync token: %s\n", c.SyncToken)
		b.WriteString("Pass it as sync_token next time to get only what changed after this point.\n")
	case c.NextPageToken != "":
		b.WriteString("This read did not finish, so there is NO sync token yet — Google issues one " +
			"with the last page only. Pass page_token to continue; the token arrives at the end.\n")
		fmt.Fprintf(&b, "page_token: %s\n", c.NextPageToken)
	default:
		b.WriteString("No sync token came back, so this read cannot be continued incrementally; " +
			"call again with no sync_token.\n")
	}

	if c.Requests > 1 {
		fmt.Fprintf(&b, "(%d API requests)\n", c.Requests)
	}
	return b.String()
}
