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
   for the run and deletes afterward. The one exception, decided on
   2026-10-09: one status event per run on the primary calendar, a year
   ahead, with an invented title, deleted by its id in the same run. A
   guard in the driver refuses every other write there.

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
  call. So are the two that widen who can see a calendar: publishing it
  to the internet or a domain, and sharing it outside the organization or
  with a new owner. `share_calendar`, registered by default, gives access
  only to named people and groups inside the organization, and otherwise
  only lowers access already given. A group rule reaches everyone in the
  group, including people added later; a member outside the
  organization too, if the organization's sharing policy lets them in.
  The server does not read a group's members before it shares, and says
  so on every group share. An event that
  declines every meeting it overlaps has a flag of its own,
  `GCAL_ENABLE_DECLINE_ALL`, and `create_event` refuses one. Tool annotations are a hint a client may ignore — a host in an
  auto-approve mode runs an annotated tool without asking anybody — so
  the real gate is that the tool is not registered at all.
- **Read-only mode requests read-only scopes**, so a write is refused by
  Google rather than by this server's own politeness.
- **It asks the person nothing.** Your client approves each call. What
  holds on every client is in the server: the registration flags,
  `confirm` on the deletes, the calendar's title on the two wider shares,
  the required `notify` and `scope`, the etag a write is made under, and
  `dry_run`. `delete_calendar`,
  `clear_calendar` and `publish_calendar` carry Claude Code's
  `requiresUserInteraction`, which makes Claude Code prompt on every call; no other client reads it, so it
  is never the control.
- **No tool changes the server's own settings.** The flags, the
  environment and the profile are the operator's, set outside the
  conversation, and a test calls every tool and holds that none of them
  changed.

## Reporting

See `SECURITY.md`.
