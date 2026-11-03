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

1. **Never commit anything internal.** No organization names, calendar
   ids or URLs, account or attendee addresses, Cloud project ids, OAuth
   client ids or secrets, and no event title, description, location or
   guest list from a real calendar, anywhere: code, fixtures, goldens,
   transcripts, commit or tag messages, logs. `make leaks` fails the
   build (D-04).
2. **Never record a fixture, and never let the live driver read what it
   did not write.** Fixtures are generated (D-05). The driver works on
   calendars it made, and writes nothing on the primary calendar but one
   status event per run (D-71, D-72). Evals read only the in-memory
   calendar (D-73).
3. **Never write to stdout outside `main`.** Stdout carries only MCP
   JSON-RPC frames, and logs go to stderr through `slog` (D-02).
   forbidigo holds it, with `scripts/` excluded.
4. **Never log the payload**: addresses, event titles, descriptions,
   locations, search terms or error messages. Method, tool, outcome,
   duration and a failed call's error class are fine (D-03).
5. **Never turn a date into an instant**, and never send a `dateTime`
   without its IANA zone. The zone comes from the call, the calendar or
   the account's settings, never the process (D-10). Every read names
   the window and zone it used (D-12).
6. **Never infer a recurrence scope.** A write to a recurring event takes
   `instance`, `series` or `this_and_following`, with no default (D-18).
   `this_and_following` is two calls, and the result says what it reset
   (D-19).
7. **Never default `notify`.** A write that can reach a person refuses
   without it (D-21), `none` is refused for a guest outside the
   organizer's domain (D-24), and no result claims what a guest received
   (D-23).
8. **Never call a PUT method.** Every write is a patch (D-27), made under
   the etag of the read that planned it, and a 412 is `[stale]` (D-28).
   The one retry: under the server's own etag, it reads again once and
   writes only if the write is unchanged (D-29).
9. **Never answer availability from a list of events.** It comes from
   `freebusy.query`; a calendar without an answer is unknown (D-34).
10. **Never import `google.golang.org/api`**; extend `internal/gcal` (D-06).
11. **Never register a tool outside `add`**, whose `Kind` decides its
    annotations and whether it registers (D-54). Destructive tools and
    the wider shares register only with `GCAL_ENABLE_DESTRUCTIVE=true`
    and still take `confirm` (D-55). Never make a risky form an argument:
    it is a tool of its own (D-49), as `create_event_declining_all` is,
    behind `GCAL_ENABLE_DECLINE_ALL` (D-44). The server asks the person
    nothing (D-56), and the `requiresUserInteraction` mark stays on the
    tools D-57 names. Do not gate `cancel_event` (D-39).
12. **Never call an API method or model a field without a verdict.**
    `testdata/api-coverage.tsv` and `testdata/api-fields.tsv` hold one
    per method and field; `make api-coverage` and `make api-fields` fail
    without one (D-08).
13. **Never rename a released tool, output field or resource URI.**
    `make schema-diff` fails on one; adding is fine (D-68).
14. **Never commit to `main` or push.** Work on a branch; the owner
    pushes.
15. **Never adopt a belief about Google from a reference page's prose.**
    Check the discovery document or a live run, and record the verdict in
    the comment at the line that depends on it, with the source link and
    the live step that checks it.

## Definition of done

- `make check` passes; its `check` target is the list. On a pull request
  CI also runs `schema-ack`, which needs a commit footer naming each
  changed item of the tool surface (`CONTRIBUTING.md`).
- New behavior has tests.
- `/simplify` and `/code-review medium` have run. A fixed finding is one
  line in the commit body. A declined one goes in the PR body, or becomes
  a present-tense constraint comment at the line it protects. Never write
  findings into `docs/`.
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
  keyring.** `TestMain` substitutes the keyring; never let a test reach
  the real one (D-69).
- **Creating a calendar spends a quota that deleting does not refund.**
  Pass `-keep` to the live driver to leave its scratch calendars for the
  next run.
- **Never delete a decision or reuse an ID.** Supersede or deprecate it,
  and take the next ID from the file's `Next ID:` line.

## Writing

Plain and short, everywhere: lead with the outcome, one idea per
sentence, no narration of the investigation, and no count a gate does
not check. History goes in commit messages. The CHANGELOG rules in `CONTRIBUTING.md` are not optional,
because `release.yml` lifts the section verbatim into the release notes.
