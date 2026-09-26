# Security and confidentiality

## What never enters this repository

No organization names, calendar ids or URLs, account or attendee email
addresses, Cloud project ids, OAuth client ids or secrets, and no event
title, description, location or guest list from a real calendar. This
holds for code, docs, fixtures, transcripts, commit messages, pull
requests and logs.

A calendar is a worse case than a document store: **every event carries
other people's email addresses**, and an attendee list is personal data
about people who never agreed to this repository existing.

Two rules make that structural rather than a matter of care:

1. **Fixtures are generated, never recorded.** `internal/gapi/caltest`
   builds everything it returns. A fixture copied from a live response is
   itself the leak, whatever a scanner says about it.
2. **The live driver reads only what it wrote**, on a calendar it creates
   for the run and deletes afterward.

`make leaks` scans the working tree and `make leaks-history` scans every
blob and commit message. The rules are an allow-list anchored on shapes
this server's own output cannot produce — an `@` with a dot-suffixed
domain, a literal Google id suffix, a known URL prefix — so a rule cannot
collide with a timestamp or an id and get dismissed as flaky.

## What logs carry

Method, tool name, outcome, duration, and a truncated calendar id.

Never: an email address, an event title, description or location, or a
search term. A search term reaches a log through a request URL, so
transport errors are stripped of their query string before anything is
logged.

This is why the issue template asks for a debug log: it is safe to paste
by construction rather than by your vigilance.

## What the server will not do

- **Stdout carries only MCP JSON-RPC frames.** Logs go to stderr. This is
  the protocol, and `forbidigo` enforces it outside `main`.
- **Destructive tools are unregistered** unless
  `GCAL_ENABLE_DESTRUCTIVE=true`, and each still needs `confirm` on the
  call. Tool annotations are a hint a client may ignore — a host in an
  auto-approve mode runs an annotated tool without asking anybody — so
  the real gate is that the tool is not registered at all.
- **Read-only mode requests read-only scopes**, so a write is refused by
  Google rather than by this server's own politeness.

## Reporting

See `SECURITY.md`.
