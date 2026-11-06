// Command gates is this repository's own checks.
//
// They are Go rather than shell for three reasons: a shell script is
// not held to gofmt, vet, lint or a test; `make check` runs on Windows
// in CI, where bash is a dependency rather than a given; and a script
// that parses JSON with sed is how a quote ends up inside a string.
//
// Every gate here also asserts a FLOOR on how much it read. "Found
// nothing" and "looked at nothing" print the same sentence otherwise,
// and a gate nobody has watched fail is not yet a gate.
//
// `go run ./scripts/gates` lists the commands and their arguments.
package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// command is one thing this program does.
type command struct {
	// run is given the words after the command's name, as many as args
	// allows.
	run func(args []string)
	// args spells those words for the usage text, and says how many the
	// command takes: a word in brackets is optional.
	args string
	doc  string
	// runsIn is the pipeline that has to run the command. The parity gate
	// holds it, and the usage text shows it.
	runsIn pipeline
}

// pipeline is where a command has to run.
type pipeline int

const (
	// manual is run by hand, and nothing has to run it. It starts at one,
	// so a command that names no pipeline is refused rather than read as
	// manual.
	manual pipeline = iota + 1
	// inCheck runs in `make check` and in CI.
	inCheck
	// inPR runs in CI on a pull request and not in `make check`: it
	// measures from the merge-base, which only a pull request has.
	inPR
	// inRelease runs in the release workflows or the goreleaser config.
	inRelease
)

func (p pipeline) String() string {
	switch p {
	case manual:
		return "manual"
	case inCheck:
		return "make check"
	case inPR:
		return "pull request"
	case inRelease:
		return "release"
	}
	return "no pipeline"
}

// commands is the one list of what this program does. The dispatch, the
// usage text, the docs gate and the parity gate all read it, so none of
// them can name a command the others do not.
//
// Filled in init rather than as a literal, because the docs gate reads
// this map and Go sees that as an initialization cycle.
var commands map[string]command

func init() {
	commands = map[string]command{
		"coverage": {
			run: coverageCmd, args: "PROFILE MIN",
			doc: "enforce the coverage floor per package", runsIn: inCheck,
		},
		"classes": {
			run: func([]string) { check(classGate(), "error classes") },
			doc: "the error vocabulary, held closed from both sides", runsIn: inCheck,
		},
		"api-coverage": {
			run: func([]string) { check(apiCoverageGate(), "API coverage") },
			doc: "every published API method is used or written off", runsIn: inCheck,
		},
		"api-fields": {
			run: func([]string) { check(apiFieldsGate(), "API field coverage") },
			doc: "every published field is modeled or written off", runsIn: inCheck,
		},
		// CI holds the snapshot this writes, not the fetch: a fetch that
		// refreshes a stable judgment runs by hand, and CI runs only checks
		// that need no network.
		"api-diff": {
			run:    func([]string) { check(apiDiff(os.Stdout), "API diff") },
			doc:    "refetch the discovery document over the network and rewrite the snapshot",
			runsIn: manual,
		},
		// What CI holds is the vendored copy rather than the fetch.
		"schema-refetch": {
			run:    func([]string) { check(schemaRefetch(os.Stdout), "vendored schemas") },
			doc:    "check the vendored schemas against what their sources serve over the network",
			runsIn: manual,
		},
		"leaks": {
			run:    func(a []string) { check(leakGate(len(a) > 0 && a[0] == "history"), "leak scan") },
			args:   "[history]",
			doc:    "identifiers and data in the working tree, or in every blob and message of the history",
			runsIn: inCheck,
		},
		"transcript": {
			run: func([]string) { check(transcriptGate(), "transcript redaction") },
			doc: "the live driver prints only through its redactor", runsIn: inCheck,
		},
		"live-cover": {
			run: func(a []string) { check(liveCoverGate(a[0]), "live driver coverage") }, args: "BIN",
			doc: "every published tool has a step in the live driver", runsIn: inCheck,
		},
		"pins": {
			run: func([]string) { check(pinGate(), "workflow pins") },
			doc: "actions pinned by SHA, tool versions exact", runsIn: inCheck,
		},
		"parity": {
			run: func([]string) { check(parityGate(), "make/CI parity") },
			doc: "`make check` and ci.yml run the same things, and each command runs where it says", runsIn: inCheck,
		},
		"smoke": {
			run: func(a []string) { check(smoke(a[0]), "stdio smoke") }, args: "BIN",
			doc: "drive the binary over stdio", runsIn: inCheck,
		},
		"schema-diff": {
			run: func(a []string) { check(schemaDiff(a[0]), "schema diff") }, args: "BIN",
			doc: "diff the tool schemas against the newest release's recorded baseline", runsIn: inCheck,
		},
		"schema-baseline": {
			run: func(a []string) { check(writeBaseline(a[0]), "schema baseline") }, args: "BIN",
			doc: "record the release being cut as the baseline, in its release commit", runsIn: manual,
		},
		"schema-ack": {
			run: func(a []string) { check(schemaAck(os.Stdout, a[0], a[1]), "schema ack") }, args: "BASE HEAD",
			doc: "every tool-surface change since the merge-base is named in a commit footer", runsIn: inPR,
		},
		"staleness": {
			run: func(a []string) { check(staleness(a[0]), "staleness") }, args: "BIN",
			doc: "the docs must match the code", runsIn: inCheck,
		},
		"docs": {
			run: func([]string) { check(docsGate(), "docs") },
			doc: "every decision, citation, named path and live step resolves", runsIn: inCheck,
		},
		"mcpb": {
			run: func([]string) { check(mcpbGate(), "bundle manifest") },
			doc: "the bundle manifest describes the bundle the packer builds", runsIn: inCheck,
		},
		"release": {
			run: func([]string) { check(releaseGate(), "release wiring") },
			doc: "the release config builds what the packer stages, and signs and uploads it", runsIn: inCheck,
		},
		"release-tag": {
			run: func(a []string) { check(releaseTag(a[0]), "release tag") }, args: "TAG",
			doc: "refuse a tag whose major version is not go.mod's", runsIn: inRelease,
		},
		"server-json": {
			run: func(a []string) {
				if err := serverJSON(a[0], a[1], os.Stdout); err != nil {
					fmt.Fprintf(os.Stderr, "server-json: %v\n", err)
					os.Exit(1)
				}
			},
			args:   "VERSION CHECKSUMS",
			doc:    "write the registry entry for a build's checksums",
			runsIn: inRelease,
		},
		// Not a gate: it WRITES the release body rather than asserting
		// anything, so it prints no "ok" line and its output is the point.
		"release-notes": {
			run: func(a []string) {
				file := ""
				if len(a) > 1 {
					file = a[1]
				}
				if err := releaseNotes(a[0], file, os.Stdout); err != nil {
					fmt.Fprintf(os.Stderr, "release-notes: %v\n", err)
					os.Exit(1)
				}
			},
			args:   "VERSION [CHANGELOG]",
			doc:    "print the CHANGELOG section a tag publishes",
			runsIn: inRelease,
		},
		// Outside `make check`: this one needs binaries that exist after a
		// build rather than names that exist in the repository.
		"mcpb-pack": {
			run:    func(a []string) { check(packMCPB(a[0], a[1], a[2]), "bundle pack") },
			args:   "DIST VERSION OUT",
			doc:    "pack the .mcpb from a built dist tree",
			runsIn: inRelease,
		},
	}
}

func main() {
	if len(os.Args) < 2 {
		fail("%s", usage())
	}
	name, args := os.Args[1], os.Args[2:]
	c, ok := commands[name]
	if !ok {
		fail("unknown gate %q\n\n%s", name, usage())
	}
	if least, most := arity(c.args); len(args) < least || len(args) > most {
		fail("usage: gates %s", synopsis(name))
	}
	root, err := repoRoot()
	if err != nil {
		fail("%v", err)
	}
	if err := os.Chdir(root); err != nil {
		fail("%v", err)
	}
	c.run(args)
}

// arity is the fewest and the most words a command's args spells.
func arity(args string) (least, most int) {
	for _, word := range strings.Fields(args) {
		most++
		if !strings.HasPrefix(word, "[") {
			least++
		}
	}
	return least, most
}

// usage is generated from commands, so it cannot fall behind them.
func usage() string {
	names := slices.Sorted(maps.Keys(commands))
	width := 0
	for _, name := range names {
		width = max(width, len(synopsis(name)))
	}
	var b strings.Builder
	b.WriteString("usage: gates COMMAND [ARGS]\n\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %-*s  %s (%s)\n", width, synopsis(name), commands[name].doc, commands[name].runsIn)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// synopsis is a command's name and the arguments it takes.
func synopsis(name string) string {
	return strings.TrimSpace(name + " " + commands[name].args)
}

// couldNotRun is a gate that could not reach a verdict, a failed build
// or git call, rather than one that found a problem. It says so, and
// exits 2 where a verdict exits 1: under `go run`, which exits 1 for
// both, the message is what tells them apart.
type couldNotRun struct{ err error }

func (e couldNotRun) Error() string { return "could not run: " + e.err.Error() }
func (e couldNotRun) Unwrap() error { return e.err }

// exitCode is 2 for a gate that could not run and 1 for a verdict.
func exitCode(err error) int {
	if errors.As(err, new(couldNotRun)) {
		return 2
	}
	return 1
}

func check(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: FAIL\n%v\n", what, err)
		os.Exit(exitCode(err))
	}
	fmt.Printf("%s: ok\n", what)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		// Not a git checkout: fall back to the working directory, so the
		// gates still run from a source archive.
		return os.Getwd()
	}
	return strings.TrimSpace(string(out)), nil
}

func coverageCmd(args []string) {
	minPct, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		fail("coverage: %v", err)
	}
	check(coverageGate(args[0], minPct), "coverage")
}
