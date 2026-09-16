# CLAUDE.md — google-calendar-mcp project instructions

Project-specific rules for Claude Code in this repository. The user's
global instructions still apply; this file adds to them.

## Mission

A production-grade Go MCP server for Google Calendar, distributed to
other people. One binary, stdio, per-user OAuth, no hosted deployment.
The design, its evidence log, the decided constraints and the phase plan
live in `docs/architecture.md`. Read it before changing the tool
surface, the time model, the recurrence model or the write path. The
server answers *when* and hands off *what*: a meeting's recording, notes
document or attachment belongs to the servers built on the Meet, Docs
and Drive APIs.

## Hard rules

1. **Nothing internal, ever.** No organisation names, calendar ids or
   URLs, account or attendee email addresses, Cloud project ids, OAuth
   client ids or secrets; no event title, description, location or guest
   list from a real calendar; and no reference to any other project,
   repository, account or machine the maintainers use. This holds for
   code, docs, fixtures, goldens, transcripts, commit and tag messages,
   pull requests and logs.

   A calendar is worse than a spreadsheet here: **every event carries
   other people's email addresses**, and an attendee list is personal
   data about people who never consented to this repository existing.

   Two of these are structural rather than a matter of care, and must
   stay that way: **fixtures are generated, never recorded**, and the
   **live driver reads only a calendar it created and filled itself**.
   `docs/architecture.md` §9.1 is the full specification, including why
   every rule in the leak gate is an allow-list anchored on a shape the
   server's own generated fields cannot take.
2. **Stdout carries only MCP JSON-RPC frames.** This is the protocol, not
   a house preference. MCP's stdio transport says the server "MUST NOT
   write anything to its `stdout` that is not a valid MCP message", and
   "MAY write UTF-8 strings to its standard error (`stderr`) for logging
   purposes" —
   <https://modelcontextprotocol.io/specification/2025-06-18/basic/transports>.
   Logs use `slog` to stderr. A stray print corrupts the JSON-RPC stream
   and the client silently stops working, which is why this is a hard
   rule rather than a style note.

   `forbidigo` enforces it: `fmt.Print*` and `os.Stdout` are forbidden
   outside `main`, which names the process's streams once and passes them
   down as `io.Writer`. `scripts/` is excluded, being maintainer tooling
   rather than the server. Check the message text when verifying it — a
   settings block that fails to load leaves forbidigo on its defaults,
   firing, looking like it works.
3. **Logs never carry the payload.** Method, tool, outcome, duration and
   a truncated calendar id are fine. Email addresses, event titles,
   descriptions, locations and search terms are not — a search term
   reaches a log through a request URL, so transport errors are stripped
   of their query string.
4. **A date is not a time.** An all-day event is carried as a date end to
   end and never becomes an instant; there is no `Date` → `Zoned`
   conversion in `internal/when`, because there is no correct one. Every
   `dateTime` carries its IANA zone, not merely an offset. The zone is
   read — from the call, else the calendar, else the user's settings —
   never from the process, and every read names which it used. §4.1.
5. **The recurrence scope is required, never inferred.** Every write to a
   recurring event takes `scope`: `instance`, `series` or
   `this_and_following`. No default; a missing scope is refused with the
   three choices and what each would do. `this_and_following` is two
   calls and resets exceptions after the target — say so in the result.
   §4.2.
6. **`notify` is required, and the server has no default.** Not `none`,
   which Google documents as able to lose events; not Google's default,
   which is the opposite on ACL rules to what it is on events. Any write
   that can reach another person refuses without an explicit choice, and
   never reports `none` as a promise of silence. §4.3.
7. **Patch always, PUT never.** `events.update`, `calendars.update`,
   `calendarList.update` and `acl.update` replace the whole resource and
   are never called — a client function for any of them fails the
   API-coverage gate. Every write is a patch under `If-Match` from the
   read that produced it; a 412 is `[stale]`, never a silent retry. §4.4.
8. **Availability comes from `freebusy.query`, never from a list.** A
   list misses events whose details the caller cannot read and ignores
   `transparency`. A calendar that errored is reported **unknown**, never
   folded into "free". §4.6.
9. **Own wire types, raw REST.** Do not import `google.golang.org/api`;
   extend `internal/gcal`. No code is imported from any other project.
10. **Destructive tools are unregistered** unless
    `GCAL_ENABLE_DESTRUCTIVE=true`, and each still needs `confirm: true`
    on the call. Those are `delete_calendar` and `clear_calendar`.
    `cancel_event` is deliberately **not** among them — §9 argues why,
    and the argument is not to be quietly reversed.
11. **Every published API method has a written verdict.** All 38 are in
    §8a, used or written off with a reason, and the gate fails on a
    method with no verdict, a client call with no row, and a verdict for
    a method that no longer exists. Field-level coverage is §8b.
12. **Branches and commits.** `main` is released code and is never pushed
    to directly, release commits included. Work on a short topic branch.
    Commit at the end of every phase with a message that says what and
    why. Pushing, opening the pull request and merging are the
    maintainer's.
13. **Verify against the discovery document or a live probe** before
    adopting a convention, and record the verdict in the evidence log in
    `docs/architecture.md` §18. A reference page's prose is not evidence.
    Nine of its twenty rows refute an assumption this design started out
    holding, and one of them reversed the most consequential decision in
    it.

## Where things go

- `cmd/google-calendar-mcp/` — subcommands and server wiring.
- `internal/config/` env plus bound flags; `internal/credentials/`
  keyring → file → env; `internal/userconfig/` non-secret profile state;
  `internal/auth/` loopback OAuth and the token source.
- `internal/gcal/` wire types; `internal/gapi/` the raw REST client, with
  `caltest/` the in-memory Calendar used by tests.
- `internal/when/` dates, zoned times and windows, no network and no
  clock of its own; `internal/recur/` RRULEs, instance expansion and the
  three scopes; `internal/model/` the server's view of a calendar, event
  and busy interval; `internal/render/` text output; `internal/plan/` (phase 2)
  typed write ops and the guards; `internal/service/` orchestration and
  policy; `internal/tools/` the MCP tools; `internal/server/` SDK wiring
  and the schema dump; `internal/redact/` the log and transcript
  redactor.
- `scripts/gates/` the repository's own checks, as Go; `scripts/livecal/`
  the live driver, which also carries the live probes of §15;
  `scripts/evals/` (phase 4) the model-facing scoring harness.
- `testdata/` synthetic fixtures, renderer goldens, the API surface
  snapshot and coverage records, and the recorded tool-schema baseline.

## Definition of done

`make check`, which is what CI runs, asserted equal by the `parity` gate:
gofmt, `go vet` including the tagged tests, golangci-lint, race tests
with an 80% floor per package, govulncheck, the licence allow-list,
gitleaks, the API method and field coverage gates, the closed
error-class gate, the leak scan, the transcript redaction gate, the live
driver coverage gate, the bundle manifest gate, the workflow pin check, a
stdio smoke test, the schema diff, and the staleness gate over README,
`docs/` and CHANGELOG. Plus tests for new behaviour, `/simplify` and
`/code-review high` with findings resolved or written down, and a look at
the schema diff for anything breaking.

Green gates are not done. Anything touching the write path or an API
response shape gets a live run before it counts, and **the transcript is
read** — a sibling's driver twice reported success while its results were
wrong.

## Working across sessions

Each phase is one session, and the session is cleared between phases. On
a fresh session: read this file, the status line and §15, §16 and §17 of
`docs/architecture.md`, `CHANGELOG.md` under `[Unreleased]`,
`git log --oneline -20` and `git status`; run `make check`; then continue
the phase §16 names, on a topic branch. Commit at the end of the phase,
say which version was tagged, and stop.

## Writing

Plain and short, everywhere it lands — code comments, commit messages,
CHANGELOG, docs, tool descriptions. Lead with the outcome. One idea per
sentence. No narration of the investigation.
