// Command gates is this repository's own checks.
//
// They are Go rather than shell for three reasons that have each cost
// something in this family of servers: a shell script is not held to
// gofmt, vet, lint or a test; `make check` runs on Windows in CI, where
// bash is a dependency rather than a given; and a script that parses
// JSON with sed is how a quote ends up inside a string.
//
// Every gate here also asserts a FLOOR on how much it read. "Found
// nothing" and "looked at nothing" print the same sentence otherwise,
// and a gate nobody has watched fail is not yet a gate.
//
//	go run ./scripts/gates coverage cov.out 80
//	go run ./scripts/gates classes
//	go run ./scripts/gates api-coverage
//	go run ./scripts/gates leaks [history]
//	go run ./scripts/gates transcript
//	go run ./scripts/gates pins
//	go run ./scripts/gates parity
//	go run ./scripts/gates smoke ./google-calendar-mcp
//	go run ./scripts/gates schema-diff ./google-calendar-mcp
//	go run ./scripts/gates staleness ./google-calendar-mcp
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: gates coverage PROFILE MIN | classes | api-coverage | api-diff | leaks [history] | " +
			"transcript | pins | parity | smoke BIN | schema-diff BIN | staleness BIN")
	}
	root, err := repoRoot()
	if err != nil {
		fail("%v", err)
	}
	if err := os.Chdir(root); err != nil {
		fail("%v", err)
	}

	switch os.Args[1] {
	case "coverage":
		coverageCmd(os.Args[2:])
	case "classes":
		check(classGate(), "error classes")
	case "api-coverage":
		check(apiCoverageGate(), "API coverage")
	case "transcript":
		check(transcriptGate(), "transcript redaction")
	case "api-diff":
		// Manual: it reaches the network. What CI holds is the snapshot
		// this writes, not the fetch itself — the standard's split
		// between a fetch that refreshes a stable judgement and a check
		// whose freshness IS the check.
		check(apiDiff(os.Stdout), "API diff")
	case "leaks":
		check(leakGate(len(os.Args) > 2 && os.Args[2] == "history"), "leak scan")
	case "pins":
		check(pinGate(), "workflow pins")
	case "parity":
		check(parityGate(), "make/CI parity")
	case "smoke":
		check(smoke(binArg()), "stdio smoke")
	case "schema-diff":
		check(schemaDiff(binArg()), "schema diff")
	case "staleness":
		check(staleness(binArg()), "staleness")
	default:
		fail("unknown gate %q", os.Args[1])
	}
}

func binArg() string {
	if len(os.Args) < 3 {
		fail("usage: gates %s BIN", os.Args[1])
	}
	return os.Args[2]
}

func check(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: FAIL\n%v\n", what, err)
		os.Exit(1)
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
	if len(args) < 2 {
		fail("usage: gates coverage PROFILE MIN")
	}
	minPct, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		fail("coverage: %v", err)
	}
	check(coverageGate(args[0], minPct), "coverage")
}
