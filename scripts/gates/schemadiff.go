package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// schemaDiff compares the binary's tool surface against the last tag's,
// so a breaking change is visible before it ships.
//
// With no previous tag it prints the surface and passes: a first release
// has nothing to diff against, and failing here would block the commit
// that creates the baseline.
func schemaDiff(bin string) error {
	current, err := dumpFrom(bin)
	if err != nil {
		return err
	}
	if len(current) < 5 {
		return fmt.Errorf("the binary published %d tools; that is not the surface", len(current))
	}

	// A tag is the best baseline, and a committed snapshot is the one
	// that exists during a phased build. Without the fallback this gate
	// reported "no previous tag" on every run from the first commit to
	// the first release — which is exactly the stretch where the tool
	// surface changes most, so it was inert when it was most needed.
	previous, against, err := baseline(current)
	if err != nil {
		return err
	}
	if previous == nil {
		fmt.Printf("  no baseline yet; %d tools in the current surface. "+
			"`make schema-baseline` records it\n", len(current))
		return nil
	}

	var removed, changed []string
	for name, prev := range previous {
		now, still := current[name]
		if !still {
			removed = append(removed, name)
			continue
		}
		if prev != now {
			changed = append(changed, name)
		}
	}
	var added []string
	for name := range current {
		if _, had := previous[name]; !had {
			added = append(added, name)
		}
	}
	sort.Strings(removed)
	sort.Strings(changed)
	sort.Strings(added)

	fmt.Printf("  against %s: %d added, %d changed, %d removed\n", against, len(added), len(changed), len(removed))
	for _, n := range added {
		fmt.Printf("    + %s\n", n)
	}
	for _, n := range changed {
		fmt.Printf("    ~ %s (input schema or description changed)\n", n)
	}
	for _, n := range removed {
		fmt.Printf("    - %s  BREAKING\n", n)
	}
	// Reported, not failed: removing a tool is sometimes right, and the
	// definition of done says a person looks at this.
	return nil
}

func dumpFrom(bin string) (map[string]string, error) {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return nil, fmt.Errorf("run %s --dump-schemas: %w", bin, err)
	}
	return parseDump(out)
}

func parseDump(data []byte) (map[string]string, error) {
	var dump struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, t := range dump.Tools {
		out[t.Name] = t.Description + "\x00" + string(t.InputSchema)
	}
	return out, nil
}

func lastTag() (string, error) {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// dumpFromTag builds the binary as it was at tag, into a temp directory.
func dumpFromTag(tag string) (map[string]string, error) {
	dir, err := os.MkdirTemp("", "schema-diff")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	worktree := dir + "/src"
	if out, err := exec.Command("git", "worktree", "add", "--detach", worktree, tag).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("worktree at %s: %w: %s", tag, err, out)
	}
	defer func() { _ = exec.Command("git", "worktree", "remove", "--force", worktree).Run() }()

	bin := dir + "/old-binary"
	build := exec.Command("go", "build", "-o", bin, "./cmd/google-calendar-mcp")
	build.Dir = worktree
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build %s: %w: %s", tag, err, out)
	}
	return dumpFrom(bin)
}

// baselineFile is the recorded tool surface, used when no tag exists.
const baselineFile = "testdata/schema-baseline.json"

// baseline returns the surface to diff against and what to call it.
//
// The tag wins when there is one: it is the surface that actually
// shipped. Otherwise the committed snapshot stands in, and refreshing it
// is the deliberate act of saying the change has been looked at — which
// is the same thing tagging says, at a smaller scale.
func baseline(current map[string]string) (map[string]string, string, error) {
	if tag, err := lastTag(); err == nil && tag != "" {
		previous, derr := dumpFromTag(tag)
		if derr != nil {
			fmt.Printf("  could not read the surface at %s (%v); falling back to %s\n",
				tag, derr, baselineFile)
		} else {
			return previous, tag, nil
		}
	}
	data, err := os.ReadFile(baselineFile)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", baselineFile, err)
	}
	previous, err := parseDump(data)
	if err != nil {
		return nil, "", fmt.Errorf("parse %s: %w", baselineFile, err)
	}
	return previous, baselineFile, nil
}

// writeBaseline records the binary's current surface as the baseline.
func writeBaseline(bin string) error {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return fmt.Errorf("run %s --dump-schemas: %w", bin, err)
	}
	if _, err := parseDump(out); err != nil {
		return fmt.Errorf("the binary did not produce a readable surface: %w", err)
	}
	if err := os.WriteFile(baselineFile, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("  recorded %s\n", baselineFile)
	return nil
}
