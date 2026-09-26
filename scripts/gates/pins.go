package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	usesRe    = regexp.MustCompile(`uses:\s*([^\s#]+)(?:\s*#\s*(.*))?`)
	shaRe     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	toolPinRe = regexp.MustCompile(`@(v[0-9][^\s"']*|latest)`)
)

// pinGate holds three claims about the workflows:
//
//  1. every action is pinned to a full 40-character commit SHA, which
//     GitHub says is "currently the only way to use an action as an
//     immutable release";
//  2. every action that INSTALLS a tool pins the tool as well — a
//     pinned wrapper around an unpinned dependency is not pinned, and
//     this reads as complete when it is not;
//  3. no tool version is `@latest`, because a green build that cannot be
//     reproduced tomorrow is not evidence.
//
// Claim 2 was written into the comment above long before anything
// checked it, and the file it applies to did not exist: this counted
// workflows and wanted two, which `ci.yml` and `codeql.yml` satisfied
// while naming "CI and release" in the failure message. A count is not a
// name, so the files are named now.
func pinGate() error {
	files, err := workflowPaths()
	if err != nil {
		return err
	}
	for _, want := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		if !contains(files, want) {
			return fmt.Errorf("%s does not exist; the workflows here are %v", want, files)
		}
	}

	var problems []string
	actions, installers := 0, 0
	workflowPins := map[int][]installerPin{}

	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // a path from the workflow glob
		if err != nil {
			return err
		}
		// Claim 2 reads the file as YAML, because the question is which
		// `with:` belongs to which `uses:` and that is structure, not
		// text. Claims 1 and 3 are about the literal lines — a SHA and
		// its trailing comment — so they stay a line scan.
		flow, err := readWorkflow(path)
		if err != nil {
			return err
		}
		found, pins := unpinnedTools(flow)
		installers += len(pins)
		for _, pin := range pins {
			workflowPins[pin.row] = append(workflowPins[pin.row], pin)
		}
		for _, problem := range found {
			problems = append(problems, path+": "+problem)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}

			if m := usesRe.FindStringSubmatch(trimmed); m != nil {
				actions++
				ref := m[1]
				// A local action is a path, not a pinned reference.
				if strings.HasPrefix(ref, "./") {
					continue
				}
				at := strings.LastIndex(ref, "@")
				if at < 0 {
					problems = append(problems, fmt.Sprintf("%s:%d: %s has no version at all", path, i+1, ref))
					continue
				}
				if !shaRe.MatchString(ref[at+1:]) {
					problems = append(problems, fmt.Sprintf(
						"%s:%d: %s is pinned to a tag or branch, not a 40-character SHA", path, i+1, ref))
					continue
				}
				if strings.TrimSpace(m[2]) == "" {
					problems = append(problems, fmt.Sprintf(
						"%s:%d: %s is pinned but has no trailing comment saying which version that SHA is",
						path, i+1, ref[:at]))
				}
			}

			// Any tool reference pinned to @latest is unpinned.
			for _, m := range toolPinRe.FindAllStringSubmatch(trimmed, -1) {
				if m[1] == "latest" {
					problems = append(problems, fmt.Sprintf(
						"%s:%d: something is pinned to @latest: %s", path, i+1, trimmed))
				}
			}
		}
	}

	if actions < 3 {
		return fmt.Errorf("found only %d action references across %d workflows; that is not a real CI setup",
			actions, len(files))
	}

	// A floor on claim 2 as well as claim 1. Every installer in the
	// table below is used by these workflows, so seeing fewer than all
	// of them means the reader stopped understanding the file rather
	// than that the file became clean.
	if installers < len(toolInstallers) {
		return fmt.Errorf("found %d tool-installing action(s) across %d workflows and the table names %d; "+
			"a workflow that cannot be read reports no problems", installers, len(files), len(toolInstallers))
	}
	drift, compared := rehearsalDrift(workflowPins, makefilePath)
	problems = append(problems, drift...)

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d workflows, %d action references, all SHA-pinned and commented; "+
		"%d tool-installing action(s) pin their tool, %d also run locally\n",
		len(files), actions, installers, compared)
	return nil
}

// installers are the actions that fetch a tool rather than do the work
// themselves, and the input that pins what they fetch.
//
// Pinning one of these to a SHA pins the WRAPPER. Without the input
// beside it the action fetches whatever is current that morning, and the
// step reads exactly like a pinned one. The standard records this
// breaking a release on two servers the same afternoon: cosign 3 landed,
// `--bundle` had become required, and the config passed the arguments
// version 2 wanted.
var toolInstallers = []struct {
	action string
	input  string
	// makeVar is the Makefile variable that pins the same tool for a
	// local run, empty when nothing here runs it locally. A rehearsal on
	// a different version of goreleaser is not a rehearsal, and the two
	// numbers sit in two files that do not mention each other.
	makeVar string
}{
	{"sigstore/cosign-installer", "cosign-release", ""},
	{"anchore/sbom-action", "syft-version", ""},
	{"goreleaser/goreleaser-action", "version", "GORELEASER"},
}

// installerPin is what one tool-installing step says about its tool.
type installerPin struct {
	row     int    // which toolInstallers entry it matched
	version string // the value of that entry's input
	pinned  bool   // whether the step set it at all
}

// installerPins finds the tool-installing steps in a workflow.
//
// Which steps those are is one rule with two readers — claim 2 below,
// and the rehearsal check, which needs the version rather than its
// absence. Two walks would let the two disagree about what a
// tool-installing step is, which is how a table with one reader grows a
// second that quietly sees less.
func installerPins(flow workflow) []installerPin {
	var out []installerPin
	for _, step := range flow.steps() {
		for i, want := range toolInstallers {
			if !strings.HasPrefix(step.Uses, want.action) {
				continue
			}
			value, found := step.input(want.input)
			out = append(out, installerPin{row: i, version: value, pinned: found})
		}
	}
	return out
}

// unpinnedTools reports any tool-installing action whose version input is
// missing or floating, along with every tool-installing step it saw.
//
// The steps are returned rather than a count so the caller can assert a
// floor — an action this never recognized and an action correctly pinned
// are the same silence otherwise — and so the rehearsal check reads the
// versions from the same walk rather than repeating it.
func unpinnedTools(flow workflow) ([]string, []installerPin) {
	var problems []string
	pins := installerPins(flow)
	for _, pin := range pins {
		want := toolInstallers[pin.row]
		switch {
		case !pin.pinned:
			problems = append(problems, fmt.Sprintf(
				"%s is pinned to a SHA and does not set %s, so the TOOL it installs is whatever is "+
					"current that morning", want.action, want.input))
		case !strings.HasPrefix(pin.version, "v") || strings.Contains(pin.version, "~") || pin.version == "latest":
			problems = append(problems, fmt.Sprintf(
				"%s sets %s to %q, which floats", want.action, want.input, pin.version))
		}
	}
	return problems, pins
}

// rehearsalDrift holds the version a maintainer runs by hand against the
// version the release runs.
//
// `make release-rehearse` exists to run what the tag runs. Both numbers
// are valid on their own, neither file mentions the other, and the
// difference shows up as a release behaving unlike every rehearsal of
// it. The pair is named in the table rather than derived from the two
// strings: a module path and an action reference have only the tool in
// common, and guessing that from the names gets `anchore/sbom-action`
// wrong — it installs syft.
func rehearsalDrift(workflowPins map[int][]installerPin, makefile string) ([]string, int) {
	var problems []string
	compared := 0
	for i, want := range toolInstallers {
		if want.makeVar == "" {
			continue
		}
		compared++
		local, err := makeVariable(makefile, want.makeVar)
		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"the table pairs %s with %s, and %v", want.action, want.makeVar, err))
			continue
		}
		_, version, found := strings.Cut(local, "@")
		if !found {
			problems = append(problems, fmt.Sprintf(
				"%s is %q, which pins no version, so nothing holds %s to it", want.makeVar, local, want.action))
			continue
		}
		// "Compared nothing" and "found nothing wrong" print the same
		// clean line otherwise: a paired row whose action no workflow
		// uses any more is a comparison that silently stopped running.
		//
		// A step that is there and pins nothing is a different sentence,
		// and claim 2 above has already said it — saying "no workflow
		// uses this" as well would name the wrong file.
		if len(workflowPins[i]) == 0 {
			problems = append(problems, fmt.Sprintf(
				"the table pairs %s with %s and no workflow step uses %s, so nothing was compared",
				want.action, want.makeVar, want.action))
			continue
		}
		for _, remote := range workflowPins[i] {
			if remote.pinned && remote.version != version {
				problems = append(problems, fmt.Sprintf(
					"%s pins %s and %s runs %s: a rehearsal on a different version is not a rehearsal",
					want.makeVar, version, want.action, remote.version))
			}
		}
	}
	return problems, compared
}
