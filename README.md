# google-calendar-mcp

[![CI](https://github.com/mmedum/google-calendar-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mmedum/google-calendar-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mmedum/google-calendar-mcp)](https://github.com/mmedum/google-calendar-mcp/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-calendar-mcp/v2.svg)](https://pkg.go.dev/github.com/mmedum/google-calendar-mcp/v2)

An MCP server for Google Calendar. One binary, stdio, per-user OAuth
against your own Google account.

It answers **when** — calendars, the events on them, and who is free. What
a meeting produces (a recording, a notes document, an attachment) belongs
to servers built on the Meet, Docs and Drive APIs.

## What makes this one different

Most calendar tooling an LLM reaches for gets three things subtly wrong.
This server is built around not getting them wrong, and each is enforced
by a test rather than by care:

- **An all-day event is a date, not a time.** It never becomes an
  instant, so it cannot land on the wrong day for anyone west of UTC.
  Every timed event carries a real IANA zone, not just an offset, because
  an offset cannot expand a repeating event across a daylight-saving
  change.
- **Every read says what it read.** The absolute window, the time zone,
  where that zone came from, and whether the list was truncated. A
  schedule that does not say which Thursday it is showing can be read as
  any Thursday.
- **Availability is not a list of events.** An event you cannot see the
  details of still makes someone busy, and an event marked "free" does
  not. A calendar that could not be read is reported as *unknown*, never
  folded into "free".

The design, its evidence log and the phase plan are in
`docs/architecture.md`.

## Status

Complete and in use: reading, writing, recurrence, availability,
calendars, sharing, the resources and the Claude Desktop bundle. The
tools are listed below, and the phase plan and what is still owed are in
`docs/architecture.md` §16.

## Install

```
go install github.com/mmedum/google-calendar-mcp/v2/cmd/google-calendar-mcp@latest
```

Or download an archive from the releases page.

## Set up

You need your own Google Cloud project and OAuth client. Nothing
deployer-specific is baked into this repository.

1. Create a project and enable the **Google Calendar API**.
2. Configure the OAuth consent screen and add the scopes
   `docs/gcp-setup.md` lists. That file is generated from the code, so it
   cannot fall behind the tool surface.
3. Create an OAuth client of type **Desktop app** and download the JSON.
4. Sign in:

```
google-calendar-mcp login --client-secret ~/path/to/client_secret.json
google-calendar-mcp doctor
```

`doctor` checks the client JSON, the stored token, the granted scopes and
whether the API answers, and names what is missing.

### Signing in over SSH

The OAuth callback reaches the loopback interface of the machine running
`login`, while your browser is on your own machine. Forward the port
first. Use `--no-browser`: it prints the authorization URL and the exact
`ssh -L` line to run, with the port it actually picked.

## Configure your MCP client

```json
{
  "mcpServers": {
    "google-calendar": {
      "command": "google-calendar-mcp",
      "env": {
        "GCAL_CLIENT_SECRET": "/path/to/client_secret.json"
      }
    }
  }
}
```

Every setting is in `docs/configuration.md`.

## Tools

| Tool | What it does |
|---|---|
| `list_calendars` | Every calendar this account can see, with ids, time zones and your access level. Start here. |
| `get_calendar` | One calendar in full, including who it is shared with. |
| `list_events` | The events on one or more calendars in a window. |
| `search_events` | Free-text search across a window. |
| `get_event` | One event, with its guests and their responses. |
| `list_instances` | The occurrences of one repeating event, with the dates that were moved or removed. |
| `list_changes` | What changed since you last looked, including **deletions** — which a list cannot report, because a deleted event simply stops matching. Hands back a sync token to pass in next time. |
| `check_availability` | When people are busy and when they are free, from Google's free/busy service rather than from a list of events. Takes an optional working-hours mask. |
| `get_settings` | The account's time zone, week start and color palette. |
| `create_event` | Create an event, one-off or repeating, with a Google Meet link if you ask for one. |
| `update_event` | Change an event. Only the fields you pass are touched. |
| `cancel_event` | Cancel an event, or one occurrence of a repeating one. |
| `move_event` | Move an event to another calendar, which changes who organizes it. |
| `respond_to_event` | Answer an invitation: accepted, declined or tentative. |
| `create_calendar` | Create a calendar of your own. |
| `manage_calendar` | Rename or re-zone a calendar, set your own color and name for it, or add and remove it from your list. |
| `list_sharing` | Who can see a calendar, and what each of them can see. |
| `share_calendar` | Give somebody access, or change the access they have. |
| `unshare_calendar` | Take somebody's access away. |
| `delete_calendar` | Delete a calendar and every event on it. Gated. |
| `clear_calendar` | Delete every event on your primary calendar. Gated. |

`GCAL_READONLY=true` registers the first eight and requests only read
scopes, so the API itself refuses a write.

`manage_calendar` covers what the API splits across two resources and
people do not: **the calendar** — its title, description, location and
time zone — is what everybody it is shared with sees, while **your
subscription** to it — the color, the name you give it, whether it is
hidden, what you are emailed about — is yours alone. Unsubscribing
removes it from your list; it deletes nothing and nobody else notices.

Three rules run through every write, and each exists because guessing is
what the surveyed servers do:

- **`notify` is required** whenever the write can reach another person,
  and there is no default in either direction. `none` is refused outright
  when a guest is outside your organization — such a guest may have no
  Google Calendar for the event to appear in, so email is the only way
  they can learn of it.
- **`scope` is required** when the event repeats: `instance`, `series` or
  `this_and_following`. "Move the standup to 10:30" is three different
  operations and the API makes them look like one.
- **Every write is a patch under `If-Match`**, so it is refused as
  `[stale]` rather than overwriting somebody who changed it first. Adding
  a guest reads the list and adds to it; it never replaces it.

`dry_run: true` on any of them reports what would change and how many
guests would be emailed, without writing.

Above 200 guests Google stops propagating individual responses, so every
result about such an event says that the RSVPs it lists are incomplete.

## Resources

For clients that attach context rather than call tools, the same content
is published as three resources:

| URI | What it carries |
|---|---|
| `gcal://calendars` | The calendar list, as `list_calendars` returns it. |
| `gcal://calendars/{calendar_id}` | One calendar, as `get_calendar` returns it. |
| `gcal://calendars/{calendar_id}/events/{event_id}` | One event, as `get_event` returns it. |

They carry no handles and take no arguments, which is why there is no
resource for a schedule: a window and a zone are not optional here, and a
URI with nowhere to state them would have to invent both.

## Working hours

`check_availability` answers over a window, and a window is one interval
— so "next week, 09:00 to 17:00" cannot be asked for as one, and the
longest gap in the answer is a fifteen-hour overnight one that passes any
`min_minutes`. `working_from`, `working_to` and `working_days` mask the
free gaps to a working week instead. There is no default: without them
every hour of the window counts, and the result always says which was
used. The mask is applied in the zone the answer is rendered in, day by
local day, so 09:00 is still 09:00 on the Sunday the clocks change.

## Meeting links

`create_event` takes `conference: true` and asks Google for a Google Meet
link. The link normally comes back with the event; Google documents the
conference as generated asynchronously, so the result may instead say it
is still being made, and then you read the event again for it. Either
way the result says which, and never reports a link it does not have. A
link can only be attached as the event is created; this server does not
add one to an event that already exists, and says so rather than failing
quietly.

## Safety

- The two tools that remove something Calendar cannot bring back —
  `delete_calendar` and `clear_calendar` — are not registered at all
  unless `GCAL_ENABLE_DESTRUCTIVE=true`, and each still needs
  `confirm: true` on the call. `cancel_event` is deliberately not behind
  that flag: canceling a meeting is what a calendar is for, Google keeps
  the record, and a gate everybody turns on protects nobody.
  `docs/architecture.md` §9 argues it. What guards it instead is the
  required `scope` and the required `notify`.
- A cancellation with `notify: none` removes the meeting from your
  calendar and leaves it on your guests'. The result says so every time.
- Sharing is the one act here whose effect leaves your account, so
  `share_calendar` requires `notify` like every other write — and here
  Google's own default is to email, the opposite of its default on an
  event. Sharing with "anyone" publishes the calendar to the whole
  internet and needs `allow_public: true`; removing the rule afterward
  stops new readers and takes nothing back from whoever already looked.
  Removing access notifies nobody, because Google offers no way to ask
  for it: they are not told, they find the calendar gone.
- `GCAL_SHARING=off` removes the three sharing tools entirely.
- Logs carry the method, tool, outcome, duration and a truncated calendar
  id. Never an email address, event title, description, location or
  search term. A debug log is safe to paste into a bug report by
  construction.

## Development

`make check` runs everything CI runs, and `make parity` asserts that
those two lists are the same. See `CONTRIBUTING.md`.

Two things are run by hand rather than by CI, because they reach outside
this repository. `make live` drives the built binary against a real
account, on a scratch calendar it creates and deletes. `make evals`
scores whether a model can complete the three tasks of
`docs/architecture.md` §3 through these tools, against the in-memory
calendar, and needs an `ANTHROPIC_API_KEY`;
`go run -tags=evals ./scripts/evals -self-check` exercises the harness
without a key or a model.

## License

Apache 2.0. See `LICENSE`.
