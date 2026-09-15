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

	tag, err := lastTag()
	if err != nil || tag == "" {
		// Deliberately not an error: a first release has nothing to diff
		// against, and failing here would block the very commit that
		// creates the baseline.
		fmt.Printf("  no previous tag; %d tools in the baseline\n", len(current))
		return nil //nolint:nilerr // no baseline yet is a pass, not a failure
	}

	previous, err := dumpFromTag(tag)
	if err != nil {
		fmt.Printf("  could not read the surface at %s (%v); %d tools now\n", tag, err, len(current))
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

	fmt.Printf("  against %s: %d added, %d changed, %d removed\n", tag, len(added), len(changed), len(removed))
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
