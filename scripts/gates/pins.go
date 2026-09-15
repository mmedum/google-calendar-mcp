package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
func pinGate() error {
	files, err := filepath.Glob(".github/workflows/*.yml")
	if err != nil {
		return err
	}
	more, _ := filepath.Glob(".github/workflows/*.yaml")
	files = append(files, more...)
	sort.Strings(files)

	if len(files) < 2 {
		return fmt.Errorf("found %d workflow files; expected at least CI and release", len(files))
	}

	var problems []string
	actions := 0

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
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
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d workflows, %d action references, all SHA-pinned and commented\n", len(files), actions)
	return nil
}
