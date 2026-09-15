package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// staleness holds the documentation against the code.
//
// Its scope is deliberately narrow and named, because the failure this
// family of gates keeps describing applies to the checker too: a
// staleness gate whose scope is a hand-maintained list is the same
// problem one level up. A sibling's checked four things and passed on
// every release while four other claims were wrong.
//
// What it holds:
//
//  1. every tool the binary publishes appears in the README's table,
//     and every tool named there exists;
//  2. every GCAL_* setting the code defines is documented, and every
//     setting documented exists;
//  3. every repository path the docs name still exists;
//  4. no version number is written in prose in the README — a badge
//     cannot go stale, a copy of a fact can.
func staleness(bin string) error {
	var problems []string

	toolProblems, tools, err := checkTools(bin)
	if err != nil {
		return err
	}
	problems = append(problems, toolProblems...)

	settingProblems, settings, err := checkSettings()
	if err != nil {
		return err
	}
	problems = append(problems, settingProblems...)

	pathProblems, paths, err := checkPaths()
	if err != nil {
		return err
	}
	problems = append(problems, pathProblems...)

	problems = append(problems, checkNoVersionInProse()...)

	// Floors.
	if tools < 5 {
		return fmt.Errorf("the binary published %d tools; that is not the phase 0 surface", tools)
	}
	if settings < 8 {
		return fmt.Errorf("found %d settings in the code; that is not the configuration surface", settings)
	}
	if paths < 5 {
		return fmt.Errorf("checked %d documented paths; the extractor is not reading the docs", paths)
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d tools, %d settings, %d documented paths: all current\n", tools, settings, paths)
	return nil
}

func checkTools(bin string) ([]string, int, error) {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return nil, 0, fmt.Errorf("run %s --dump-schemas: %w", bin, err)
	}
	var dump struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &dump); err != nil {
		return nil, 0, err
	}

	readme, err := os.ReadFile("README.md")
	if err != nil {
		return nil, 0, err
	}
	text := string(readme)

	var problems []string
	published := map[string]bool{}
	for _, t := range dump.Tools {
		published[t.Name] = true
		if !strings.Contains(text, "`"+t.Name+"`") {
			problems = append(problems, fmt.Sprintf("README does not mention the tool %s", t.Name))
		}
	}
	// And the other direction: a tool the README names that no longer
	// exists.
	for _, m := range regexp.MustCompile("`([a-z][a-z0-9_]{3,})`").FindAllStringSubmatch(text, -1) {
		name := m[1]
		if !strings.Contains(name, "_") {
			continue
		}
		if strings.HasPrefix(name, "gcal_") || strings.Contains(name, "calendar_mcp") {
			continue
		}
		if looksLikeToolName(name) && !published[name] {
			problems = append(problems, fmt.Sprintf("README names %s, which the binary does not publish", name))
		}
	}
	return problems, len(dump.Tools), nil
}

// looksLikeToolName keeps the reverse check from firing on every
// snake_case word in the prose. It is a verb_noun shape.
func looksLikeToolName(s string) bool {
	verbs := []string{"list_", "get_", "search_", "create_", "update_", "cancel_", "move_",
		"respond_", "manage_", "share_", "unshare_", "delete_", "clear_", "check_"}
	for _, v := range verbs {
		if strings.HasPrefix(s, v) {
			return true
		}
	}
	return false
}

var defRe = regexp.MustCompile(`def\(&s\.\w+,\s*"[^"]+",\s*"([A-Z_0-9]+)"`)

func checkSettings() ([]string, int, error) {
	src, err := os.ReadFile("internal/config/config.go")
	if err != nil {
		return nil, 0, err
	}
	docs, err := os.ReadFile("docs/configuration.md")
	if err != nil {
		return nil, 0, err
	}
	text := string(docs)

	var problems []string
	defined := map[string]bool{}
	for _, m := range defRe.FindAllSubmatch(src, -1) {
		name := "GCAL_" + string(m[1])
		defined[name] = true
		if !strings.Contains(text, name) {
			problems = append(problems, fmt.Sprintf("docs/configuration.md does not document %s", name))
		}
	}
	// Settings the docs invent.
	for _, m := range regexp.MustCompile(`GCAL_[A-Z_0-9]+`).FindAllString(text, -1) {
		switch m {
		// Not flag-bound: these are read directly, and are documented
		// deliberately.
		case "GCAL_CONFIG_DIR", "GCAL_REFRESH_TOKEN":
			continue
		}
		if !defined[m] {
			problems = append(problems, fmt.Sprintf("docs/configuration.md documents %s, which the code does not define", m))
		}
	}
	return problems, len(defined), nil
}

// pathRe finds anything shaped like a repository path in prose.
//
// Expect the naive version of this to be mostly false positives and
// budget for the triage: a sibling's first pass reported 13 broken
// references and every one was the extractor's fault. The exclusions
// below are that triage, not a widening.
var pathRe = regexp.MustCompile("`((?:cmd|internal|scripts|docs|testdata|packaging|\\.github)/[A-Za-z0-9_./-]+)`")

func checkPaths() ([]string, int, error) {
	files, err := docFiles()
	if err != nil {
		return nil, 0, err
	}
	var problems []string
	checked := 0
	seen := map[string]bool{}

	for _, doc := range files {
		data, err := os.ReadFile(doc)
		if err != nil {
			continue
		}
		for _, m := range pathRe.FindAllStringSubmatch(string(data), -1) {
			p := strings.TrimSuffix(m[1], "/")
			key := doc + "|" + p
			if seen[key] {
				continue
			}
			seen[key] = true
			checked++
			if _, err := os.Stat(p); err != nil {
				problems = append(problems, fmt.Sprintf("%s names %s, which does not exist", doc, p))
			}
		}
	}
	sort.Strings(problems)
	return problems, checked, nil
}

func docFiles() ([]string, error) {
	out := []string{"README.md", "CONTRIBUTING.md"}
	entries, err := os.ReadDir("docs")
	if err != nil {
		return out, nil //nolint:nilerr // no docs directory is not a staleness failure
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, "docs/"+e.Name())
		}
	}
	return out, nil
}

// versionInProseRe catches a version written into the README's text.
//
// A badge shows the version, updates itself and cannot be wrong. A
// number in prose is a copy of a fact, and a copy goes stale: a
// sibling's README said v0.5.0 five releases later, and the first fix
// was a gate to keep the copy correct — machinery to maintain a
// duplicate instead of deleting it.
var versionInProseRe = regexp.MustCompile(`(?m)^[^|\[]*\bv[0-9]+\.[0-9]+\.[0-9]+\b`)

func checkNoVersionInProse() []string {
	data, err := os.ReadFile("README.md")
	if err != nil {
		return nil
	}
	var problems []string
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		// Badges, links and code blocks carry versions legitimately.
		if strings.HasPrefix(trimmed, "[!") || strings.HasPrefix(trimmed, "[") ||
			strings.HasPrefix(trimmed, "```") || strings.Contains(trimmed, "http") ||
			strings.HasPrefix(trimmed, "go install") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if versionInProseRe.MatchString(line) {
			problems = append(problems, fmt.Sprintf(
				"README.md:%d writes a version in prose; use the release badge, which cannot go stale: %q",
				i+1, trimmed))
		}
	}
	return problems
}
