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
| `make licenses` | the dependency licence allow-list |
| `make secrets` | gitleaks |
| `make api-coverage` | every published API method is used or written off, with a reason |
| `make classes` | the error vocabulary is closed **from both sides** |
| `make leaks` | no deployer-specific data in the tree |
| `make pins` | actions pinned to SHAs, tools pinned to versions |
| `make parity` | `make check` and `ci.yml` run the same things |
| `make schema-diff` | the tool surface against the last tag |
| `make smoke` | the binary over stdio, and a clean exit on disconnect |
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

## Adding a tool

1. Add the handler in `internal/tools`, with a `Kind` that decides its
   annotations and whether it registers at all.
2. Put the logic in `internal/service`, not the handler. The handlers are
   thin so the rules are testable in one place.
3. Add it to the README's tool table, or `make staleness` fails.
4. Look at `make schema-diff` for anything breaking.

## Adding an API call

`make api-coverage` fails on a client method with no verdict. Add a row
to `testdata/api-coverage.tsv` saying `used` with the method that
implements it, or `out` with why not.
