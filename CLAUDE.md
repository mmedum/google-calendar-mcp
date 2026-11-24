# CLAUDE.md — google-calendar-mcp

A Go MCP server that exposes Google Calendar over stdio: one binary per
person, per-user OAuth, nothing hosted. It answers *when* and hands off
*what* to the Drive and Docs servers (D-09). Why the code is the way it
is lives in `docs/architecture.md`, one decision per `D-NN`; read it
before changing the tool surface, the time model, the write path or the
auth wiring.

## Commands

- `make check` runs every gate CI runs; `make parity` holds the two lists
  equal.
- `make live` drives the built binary against a real account. Read the
  transcript; `CONTRIBUTING.md` says what a run does to the account.
- `make evals` scores a model and needs `ANTHROPIC_API_KEY`;
  `make evals-check` runs the scorers with no key.
- `make api-diff` refetches Google's discovery document, and
  `make release-rehearse` builds the release without publishing it.
- `go run ./scripts/gates` prints the gates; add a gate's name to run
  that one.

## Hard rules

1. **Keep everything internal out of the repository**, because it is
   public: organization names, calendar ids or URLs, account or attendee
   addresses, Cloud project ids, OAuth client ids or secrets, and any
   event title, description, location or guest list from a real
   calendar. That holds everywhere, including code, fixtures, goldens,
   transcripts, commit or tag messages, PR bodies and logs. `make leaks`
   fails the build (D-04).
2. **Generate fixtures rather than record them, and let the live driver
   read only what it wrote.** Fixtures are generated (D-05), because a
   fixture copied from a live response is itself the leak. The driver
   works on calendars it made, and writes on the primary calendar only
   one status event per run (D-71, D-72). Evals read only the in-memory
   calendar (D-73).
3. **Write to stdout only from `main`.** Stdout carries only MCP
   JSON-RPC frames, because a stray print corrupts the stream, and logs
   go to stderr through `slog` (D-02). forbidigo holds it, with
   `cmd/google-calendar-mcp/` and `scripts/` excluded.
4. **Log the call, not the payload**: leave out addresses, event titles,
   descriptions, locations, search terms, error messages and a guest
   count in a context that identifies the event. Method, tool, outcome,
   duration and a failed call's error class are fine (D-03). A calendar is other people's data.
5. **Keep a date a date rather than turning it into an instant, and send
   every `dateTime` with its IANA zone.** An all-day event read as UTC
   midnight lands a day early for everyone west of UTC. The zone comes
   from the call, the calendar or the account's settings, not the
   process (D-10). Every read names the window and zone it used (D-12).
6. **Take the recurrence scope from the caller rather than infer it.** A
   write to a recurring event takes `instance`, `series` or
   `this_and_following`, with no default (D-18). `this_and_following` is
   two calls, and the result says what it reset (D-19).
7. **Require `notify` from the caller, with no default.** A write that
   can reach a person refuses without it (D-21), `none` is refused for a
   guest outside the organizer's domain (D-24), and a result says what
   was asked of Google rather than what a guest received (D-23).
8. **Change an existing resource with a patch rather than a PUT**
   (D-27), because a PUT replaces the whole resource. Each change is made
   under the etag of the read that planned it, and a 412 is `[stale]`
   (D-28). The one retry: under the server's own etag, it reads again
   once and writes only if the new plan makes the same changes and
   reaches the same guests (D-29).
9. **Answer availability from `freebusy.query`, not from a list of
   events**, because a list misses every event whose details the caller
   cannot read and counts an event marked free as busy. A calendar
   without an answer is unknown (D-34).
10. **Extend `internal/gcal` rather than import `google.golang.org/api`**,
    which drags in gRPC and telemetry for a handful of JSON calls (D-06).
11. **Register every tool through `add`**, whose `Kind` decides its
    annotations and whether it registers (D-54). Destructive tools and
    the wider shares register only with `GCAL_ENABLE_DESTRUCTIVE=true`,
    those shares only with sharing on as well, and each still takes
    `confirm` (D-55). Make a risky form a tool of its own
    rather than an argument (D-49), as `create_event_declining_all` is,
    behind `GCAL_ENABLE_DECLINE_ALL` (D-44), because clients remember an
    approval per tool, not per argument value. The server leaves
    per-call approval to the client rather than asking the person
    (D-56), and the `requiresUserInteraction` mark stays on the tools
    D-57 names. Keep `cancel_event` an ordinary write, outside the
    destructive flag, because a gate everybody turns on protects nobody
    (D-39).
12. **Record a verdict before you call an API method or model a field.**
    `testdata/api-coverage.tsv` and `testdata/api-fields.tsv` hold one
    per method and field; `make api-coverage` and `make api-fields` fail
    without one (D-08).
13. **Keep every released tool, output field and resource URI under its
    name and shape**, because the released surface is a contract.
    `make schema-diff` fails on a removal, a rename, a narrowed input or
    an input newly required; adding is fine (D-68).
14. **Commit to a branch, not `main`, and leave the push to the owner.**
15. **Check a belief about Google against the discovery document or a
    live run, not a reference page's prose**, and record the verdict in
    the comment at the line that depends on it, with the source link and
    the live step that checks it.

## Definition of done

- `make check` passes; its `check` target is the list. On a pull request
  CI also runs `schema-ack`, which needs a commit footer naming each
  changed item of the tool surface, and `changelog`, which needs an
  entry under `[Unreleased]` or a `Changelog: none` footer
  (`CONTRIBUTING.md`).
- New behavior has tests.
- `/simplify` and `/code-review medium` have run. A fixed finding is one
  line in the commit body. A declined one goes in the PR body, or becomes
  a present-tense constraint comment at the line it protects. Findings
  go there, not into `docs/`.
- The schema diff has been read for anything breaking.
- Anything touching the write path or an API response shape has had a
  `make live` run whose transcript was read (D-70). A release also needs
  `make api-diff` and `make schema-refetch` (`docs/release.md`).

## Traps

- **Green gates are not done.** Every fake in `internal/gapi/caltest` is
  written from what we believe Google does, and only `make live` tests
  that belief. A pass count is not a read transcript: the driver's
  `-show` prints the bodies of the steps it names.
- **A forbidigo settings block that fails to load leaves forbidigo on its
  defaults**, still firing and looking right. Check the message text when
  you verify it.
- **Redirecting the config directory does not isolate a test from the OS
  keyring.** `TestMain` substitutes the keyring; keep every test on the
  substitute rather than the real one (D-69).
- **Creating a calendar spends a quota that deleting does not refund.**
  Pass `-keep` to the live driver to leave its scratch calendars for the
  next run.
- **Supersede or deprecate a decision rather than delete it, and take a
  fresh ID rather than reuse one.** The next ID is on the file's
  `Next ID:` line.

## Writing

Plain and short, everywhere: lead with the outcome, one idea per
sentence, no narration of the investigation, and no count a gate does
not check. History goes in commit messages. Follow the CHANGELOG rules
in `CONTRIBUTING.md` to the letter, because `release.yml` lifts the
section verbatim into the release notes.
