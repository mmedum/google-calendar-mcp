# google-calendar-mcp

[![CI](https://github.com/mmedum/google-calendar-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mmedum/google-calendar-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mmedum/google-calendar-mcp)](https://github.com/mmedum/google-calendar-mcp/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-calendar-mcp.svg)](https://pkg.go.dev/github.com/mmedum/google-calendar-mcp)

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

Phase 0: the read surface. Six tools, listed below. Writing events,
recurrence, availability and sharing are phases 1 to 3 — see
`docs/architecture.md` §16.

## Install

```
go install github.com/mmedum/google-calendar-mcp/cmd/google-calendar-mcp@latest
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
| `check_availability` | When people are busy and when they are free, from Google's free/busy service rather than from a list of events. |
| `get_settings` | The account's time zone, week start and colour palette. |
| `create_event` | Create an event, one-off or repeating. |
| `update_event` | Change an event. Only the fields you pass are touched. |
| `cancel_event` | Cancel an event, or one occurrence of a repeating one. |
| `move_event` | Move an event to another calendar, which changes who organises it. |
| `respond_to_event` | Answer an invitation: accepted, declined or tentative. |

`GCAL_READONLY=true` registers the first eight and requests only read
scopes, so the API itself refuses a write.

Three rules run through every write, and each exists because guessing is
what the surveyed servers do:

- **`notify` is required** whenever the write can reach another person,
  and there is no default in either direction. `none` is refused outright
  when a guest is outside your organisation — such a guest may have no
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

## Safety

- The two tools that remove something Calendar cannot bring back —
  deleting a calendar, and clearing every event from one — arrive in a
  later phase and will be unregistered unless
  `GCAL_ENABLE_DESTRUCTIVE=true`, and will still need `confirm` on the
  call. `cancel_event` is deliberately not behind that flag: cancelling a
  meeting is what a calendar is for, Google keeps the record, and a gate
  everybody turns on protects nobody. `docs/architecture.md` §9 argues
  it. What guards it instead is the required `scope` and the required
  `notify`.
- A cancellation with `notify: none` removes the meeting from your
  calendar and leaves it on your guests'. The result says so every time.
- `GCAL_SHARING=off` removes the sharing tools entirely.
- Logs carry the method, tool, outcome, duration and a truncated calendar
  id. Never an email address, event title, description, location or
  search term. A debug log is safe to paste into a bug report by
  construction.

## Development

`make check` runs everything CI runs, and `make parity` asserts that
those two lists are the same. See `CONTRIBUTING.md`.

## Licence

Apache 2.0. See `LICENSE`.
