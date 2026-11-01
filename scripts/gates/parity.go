package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// parityGate asserts that `make check` and CI run the same things.
//
// The two lists live in different files and you are only ever editing
// one of them. Three of the four servers in this family had them
// diverged, usually with the local one ahead — which means the
// build-tagged files compile only on a maintainer's laptop.
//
// A comment in the Makefile claiming "everything CI runs" is the
// sentence that stops the next person checking, so this checks.
func parityGate() error {
	targets, vetTags, err := makeCheckTargets()
	if err != nil {
		return err
	}
	ciSteps, ciVetTags, err := ciRunSteps()
	if err != nil {
		return err
	}

	if len(targets) < 8 {
		return fmt.Errorf("`make check` names only %d targets; that is not the gate set", len(targets))
	}
	if len(ciSteps) < 8 {
		return fmt.Errorf("ci.yml runs only %d make targets; that is not the gate set", len(ciSteps))
	}

	var problems []string
	for _, t := range targets {
		if !ciSteps[t] {
			problems = append(problems, fmt.Sprintf("`make check` runs %q and CI does not", t))
		}
	}
	var ciOnly []string
	for t := range ciSteps {
		if !contains(targets, t) && prOnly[t] == "" {
			ciOnly = append(ciOnly, t)
		}
	}
	sort.Strings(ciOnly)
	for _, t := range ciOnly {
		problems = append(problems, fmt.Sprintf("CI runs `make %s` and `make check` does not", t))
	}
	problems = append(problems, prOnlyProblems(targets, ciSteps)...)

	// The specific divergence that leaves build-tagged files compiling
	// only on a maintainer's laptop.
	//
	// Map the tag to the TARGET that declares it and check CI runs that
	// target, rather than grepping CI for the tag. CI here invokes make
	// targets, so a tagged vet inside `vet:` is covered when CI runs
	// `make vet` — and the first version of this check, which looked for
	// the literal string in ci.yml, reported that as a divergence when
	// the two agreed perfectly. Matching names instead of mapping
	// targets is the mistake the standard warns about, made here.
	for tag, target := range vetTags {
		if ciVetTags[tag] || ciSteps[target] {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"the Makefile vets with -tags=%s in the %q target and CI runs neither that target nor the tag, "+
				"so those files compile only locally", tag, target))
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d targets in `make check`, all run by CI; %d vet tag(s) covered\n", len(targets), len(vetTags))
	return nil
}

// prOnly is each target only pull-request CI runs, with the reason it
// cannot be in `make check`.
var prOnly = map[string]string{
	"schema-ack": "it measures a pull request from its merge-base, which only a pull request has",
}

// prOnlyProblems holds each pull-request target to a CI step and keeps it
// out of `make check`. Without it, deleting the step leaves every gate
// green.
func prOnlyProblems(targets []string, ciSteps map[string]bool) []string {
	var problems []string
	for _, t := range sortedKeys(prOnly) {
		if !ciSteps[t] {
			problems = append(problems, fmt.Sprintf("CI does not run `make %s`, which only a pull request can: %s", t, prOnly[t]))
		}
		if contains(targets, t) {
			problems = append(problems, fmt.Sprintf("`make check` runs %q, which only a pull request can: %s", t, prOnly[t]))
		}
	}
	return problems
}

var (
	checkRe  = regexp.MustCompile(`(?m)^check:\s*([^#\n]*)`)
	vetTagRe = regexp.MustCompile(`vet\s+-tags=(\w+)`)
	makeRe   = regexp.MustCompile(`make\s+([a-z][a-z0-9-]*)`)
	targetRe = regexp.MustCompile(`^([a-z][a-z0-9-]*):`)
)

// makeCheckTargets returns what `make check` runs, and a map from each
// build tag vetted anywhere in the Makefile to the target that declares
// it.
func makeCheckTargets() ([]string, map[string]string, error) {
	data, err := os.ReadFile("Makefile")
	if err != nil {
		return nil, nil, err
	}
	m := checkRe.FindSubmatch(data)
	if m == nil {
		return nil, nil, fmt.Errorf("no `check:` target in the Makefile")
	}
	var targets []string
	targets = append(targets, strings.Fields(string(m[1]))...)
	sort.Strings(targets)

	// Walk the file tracking which target's recipe we are inside, so a
	// tagged vet is attributed to the target that would run it.
	tags := map[string]string{}
	current := ""
	for _, line := range strings.Split(string(data), "\n") {
		if m := targetRe.FindStringSubmatch(line); m != nil {
			current = m[1]
			continue
		}
		for _, t := range vetTagRe.FindAllStringSubmatch(line, -1) {
			if _, seen := tags[t[1]]; !seen {
				tags[t[1]] = current
			}
		}
	}
	return targets, tags, nil
}

func ciRunSteps() (map[string]bool, map[string]bool, error) {
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		return nil, nil, err
	}
	run := runText(string(data))
	steps := map[string]bool{}
	for _, m := range makeRe.FindAllStringSubmatch(run, -1) {
		steps[m[1]] = true
	}
	tags := map[string]bool{}
	for _, m := range vetTagRe.FindAllStringSubmatch(run, -1) {
		tags[m[1]] = true
	}
	return steps, tags, nil
}

// runText is every command a workflow's `run:` steps carry, a one-line
// `run:` and the lines of a `run: |` block alike, without comments. Only
// a `run:` step runs anything: a comment naming a target does not.
func runText(ci string) string {
	var out []string
	block := -1 // the indentation of the `run:` whose block we are in
	for _, line := range strings.Split(ci, "\n") {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if block >= 0 {
			if trimmed == "" || indent > block {
				if !strings.HasPrefix(trimmed, "#") {
					out = append(out, trimmed)
				}
				continue
			}
			block = -1
		}
		step := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		cmd, ok := strings.CutPrefix(step, "run:")
		if !ok {
			continue
		}
		cmd = strings.TrimSpace(cmd)
		if strings.HasPrefix(cmd, "|") || strings.HasPrefix(cmd, ">") {
			block = indent + len(trimmed) - len(step)
			continue
		}
		out = append(out, cmd)
	}
	return strings.Join(out, "\n")
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
