# Contributing

Thanks for looking. A few things are non-obvious.

## Before anything else

```
make check
```

That is the full gate set and it is what CI runs. `make parity` asserts
those two lists are the same, so if you add a gate, add it to both.

`docs/development.md` explains what each gate holds and why.

## The rules that are not style

Three things in this repository are correctness rather than preference,
and a change that breaks one will be sent back:

1. **Stdout carries only MCP JSON-RPC frames.** Logs use `slog` to
   stderr. `forbidigo` enforces it outside `main`, which is the one place
   that names the process's streams.
2. **An all-day event is a date, never an instant.** There is no
   `Date` → `Zoned` conversion in `internal/when` because there is no
   correct one. If you need an instant, take a zone as an argument and
   say so in the result.
3. **Nothing deployer-specific enters the repository.** No real calendar
   ids, email addresses, event content or Cloud project ids — in code,
   docs, fixtures, tests or commit messages. Fixtures are generated, not
   recorded.

## Before you change the tool surface

Read `docs/architecture.md`. It carries the design, the decided
constraints, and an evidence log where every convention is recorded with
how it was verified. If you are about to adopt a convention, verify it
against the discovery document or a live probe and add a row — a
reference page's prose is not evidence.

## Adding an API call

`make api-coverage` fails on a client method with no verdict. Add a row
to `testdata/api-coverage.tsv`: `used` with the method that implements
it, or `out` with why this server does not call it.

## Commits and branches

`main` is released code and takes pull requests only. Work on a short
topic branch and write a message that says what and why.

## Reporting a bug

Use the issue template. It asks for `doctor` output, a version and a
debug log, all of which are safe to paste: the log carries no event
content by construction. Please do not paste event titles, guest lists or
your OAuth client JSON.
