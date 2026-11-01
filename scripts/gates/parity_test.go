package main

import (
	"strings"
	"testing"
)

// A pull-request target runs in CI and stays out of `make check`, which
// has no merge-base to measure from.
func TestParityHoldsThePullRequestTargets(t *testing.T) {
	check := []string{"fmt", "vet", "schema-diff"}
	for _, tc := range []struct {
		name    string
		targets []string
		ci      map[string]bool
		want    string
	}{
		{"in CI only", check, map[string]bool{"schema-ack": true}, ""},
		{"missing from CI", check, map[string]bool{}, "CI does not run `make schema-ack`"},
		{"in make check", append(check, "schema-ack"), map[string]bool{"schema-ack": true}, "`make check` runs \"schema-ack\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(prOnlyProblems(tc.targets, tc.ci), "\n")
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Errorf("problems = %q, want %q", got, tc.want)
			}
		})
	}
}

// Only a `run:` step runs a target, a one-line step or a block; a
// comment that names one does not, and neither does a vet tag in one.
func TestRunTextReadsOnlyRunSteps(t *testing.T) {
	ci := `jobs:
  pr:
    steps:
      - name: schema change acknowledged
        run: make schema-ack
      # make smoke and go vet -tags=live run in the test job
      - run: |
          make cover
          # make lint is its own job
          go vet -tags=integration ./...
      - name: after the block
        run: make fmt`
	run := runText(ci)
	for _, want := range []string{"make schema-ack", "make cover", "go vet -tags=integration ./...", "make fmt"} {
		if !strings.Contains(run, want) {
			t.Errorf("runText missed %q:\n%s", want, run)
		}
	}
	for _, not := range []string{"make smoke", "-tags=live", "make lint"} {
		if strings.Contains(run, not) {
			t.Errorf("runText read %q from a comment:\n%s", not, run)
		}
	}
}
