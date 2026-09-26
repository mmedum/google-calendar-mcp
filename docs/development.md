# Development

## The one command

```
make check
```

That is what CI runs, and `make parity` asserts those two lists are the
same rather than a comment claiming it. Three of the four servers in this
family had them diverged at one point, usually with the local one ahead —
which means build-tagged files compile only on a maintainer's laptop.

## The gates, and what each one is for

| Target | Holds |
|---|---|
| `make fmt` | gofmt would change nothing |
| `make vet` | `go vet`, including the `live` build tag so tagged files keep compiling |
| `make tidy` | `go.mod` and `go.sum` are what `go mod tidy` would write |
| `make lint` | golangci-lint, including `forbidigo` for the stdout rule |
| `make cover` | race tests, and an 80% floor **per package**, not on the average |
| `make vuln` | govulncheck |
| `make licenses` | the dependency license allow-list |
| `make secrets` | gitleaks |
| `make api-coverage` | every published API method is used or written off, with a reason |
| `make api-fields` | every published field of the four main resources is modeled or written off, with a reason |
| `make classes` | the error vocabulary is closed **from both sides** |
| `make leaks` | no deployer-specific data in the tree |
| `make pins` | actions pinned to SHAs, tools pinned to versions, and a tool run locally pinned to the version the release runs |
| `make live-cover` | every published tool has a step in the live driver |
| `make parity` | `make check` and `ci.yml` run the same things |
| `make schema-diff` | the tool surface against the last tag |
| `make smoke` | the binary over stdio, and a clean exit on disconnect |
| `make mcpb` | the bundle manifest describes the bundle the packer stages |
| `make release` | `.goreleaser.yaml` builds what the packer stages, and signs and uploads it; `release.yml` runs `gates release-tag` before goreleaser |
| `make staleness` | README, `docs/` and the code agree |

Two are manual because they need the network:

- `make api-diff` refetches the Calendar discovery document and rewrites
  `testdata/api-surface.json`. On a network failure it fails loudly and
  leaves the committed file untouched. Run it at release time; what CI
  holds is the committed snapshot.
- `make leaks-history` scans every blob and commit message. Run it before
  going public.

## Every gate asserts a floor

"Found nothing" and "looked at nothing" print the same sentence, so each
gate also asserts how much it read: a coverage profile with too few
statement lines, a leak scan over too few files, an API snapshot with too
few methods. A gate nobody has watched fail is not yet a gate — break one
deliberately and watch it before you trust it.

## Green gates are not done

Anything touching the write path or an API response shape gets a live run
before it counts, and **the transcript is read**. A sibling's driver
twice reported success while its results were wrong.

```
make live                     # every step, against the logged-in account
make live && read the output  # the part that matters
```

What it does to the account, so nothing is a surprise:

- It creates a scratch calendar, or **adopts and empties** one an earlier
  run left, and creates a second for the move steps. Calendar creation is
  quota-counted and deleting one does not refund it, so `-keep` leaves
  them for the next run: `go run -tags=live ./scripts/livecal -bin
  ./google-calendar-mcp -keep`.
- The calendar and sharing steps make a third calendar through
  `create_calendar` and remove it through `delete_calendar`, which costs
  one creation per run and is the only way to drive those two tools at
  all. The driver starts the server with `GCAL_ENABLE_DESTRUCTIVE=true`
  for them; every step that could reach a destructive tool names a
  calendar the driver created, and a step whose calendar was never
  created is skipped rather than called.
- **Nothing reaches another person unless you ask for it.** Every event
  the driver writes has no guests but the account itself, and the one
  address it shares a calendar with is in `example.test`, which cannot
  resolve. `-spike-notify` is what arms the steps and spikes that mail a
  real person, and they say so before they run.
- `-show <substring>` prints the redacted body of every step whose name
  contains it. A pass/fail line cannot show a result that is confidently
  wrong, which is how the last three phases each found a defect.

## Cutting a release

That is `docs/release.md`: what the tag does, what to check afterward,
and the three steps no rehearsal reaches.

## Adding a tool

1. Add the handler in `internal/tools`, with a `Kind` that decides its
   annotations and whether it registers at all.
2. Put the logic in `internal/service`, not the handler. The handlers are
   thin so the rules are testable in one place.
3. Add it to the README's tool table, or `make staleness` fails.
4. Add a step to `scripts/livecal`, or `make live-cover` fails. A tool
   with no live step is the one nobody remembers to drive: the fake
   answers it, every test passes, and the first real call is a user's.
5. Look at `make schema-diff` for anything breaking.

## Adding an API call

`make api-coverage` fails on a client method with no verdict. Add a row
to `testdata/api-coverage.tsv` saying `used` with the method that
implements it, or `out` with why not.

## Adding a wire field

`make api-fields` fails on a field of `Event`, `Calendar`,
`CalendarListEntry` or `AclRule` that `internal/gcal` carries with no row
in `testdata/api-fields.tsv`, and on a row saying `out` for a field the
code actually reads. Fields come from the discovery document through
`make api-diff`, so a field Google adds arrives as a gate failure naming
it rather than as silence.

## Renderer golden files

`testdata/golden/` holds what each renderer prints. A change to how a
schedule reads shows up as a diff somebody has to look at:

```
go test ./internal/render -update
```

Regenerating is one flag; reading the diff is the part that matters.
