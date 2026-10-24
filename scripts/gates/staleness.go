package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
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
//     every tool named there exists, and every Claude Code permission
//     rule the README gives names a tool the binary publishes;
//  2. every GCAL_* variable the server reads is documented by name, a
//     removed one included, no other is, and the configuration page
//     states the profile rule;
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
	// Raised from 5 when the roots became derived rather than listed:
	// the docs name dozens of paths, and a floor an accident could clear
	// is not a floor.
	if paths < 25 {
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
	var names []string
	for _, t := range dump.Tools {
		names = append(names, t.Name)
	}
	return readmeToolProblems(string(readme), names), len(dump.Tools), nil
}

// readmeToolProblems holds the README against the tools the binary
// publishes, both ways, and holds its permission rules to them.
func readmeToolProblems(text string, tools []string) []string {
	var problems []string
	published := map[string]bool{}
	for _, name := range tools {
		published[name] = true
		if !strings.Contains(text, "`"+name+"`") {
			problems = append(problems, fmt.Sprintf("README does not mention the tool %s", name))
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
	// A permission rule for a tool that does not exist matches nothing,
	// so the person it was meant to ask is never asked.
	for _, m := range permissionRuleRe.FindAllStringSubmatch(text, -1) {
		if !published[m[1]] {
			problems = append(problems, fmt.Sprintf("README gives a permission rule for %s, which the binary does not publish", m[1]))
		}
	}
	return problems
}

// permissionRuleRe is a Claude Code permission rule for one of this
// server's tools, under the server name the README configures.
var permissionRuleRe = regexp.MustCompile(`mcp__google-calendar__([a-z0-9_]+)`)

// looksLikeToolName keeps the reverse check from firing on every
// snake_case word in the prose. It is a verb_noun shape.
func looksLikeToolName(s string) bool {
	verbs := []string{"list_", "get_", "search_", "create_", "update_", "cancel_", "move_",
		"respond_", "manage_", "share_", "unshare_", "publish_", "widen_", "delete_", "clear_", "check_"}
	for _, v := range verbs {
		if strings.HasPrefix(s, v) {
			return true
		}
	}
	return false
}

// configDocPath is the page every setting is documented on.
const configDocPath = "docs/configuration.md"

func checkSettings() ([]string, int, error) {
	docs, err := os.ReadFile(configDocPath)
	if err != nil {
		return nil, 0, err
	}
	vars := config.Vars()
	return configDocProblems(vars, string(docs)), len(vars), nil
}

// configDocProblems holds the configuration page, text, to the variables
// the server reads, vars, both ways. It names every one as a whole word,
// a removed one it refuses and the two read outside Define included,
// names no other, and says what a profile name is in the words the
// server refuses one with.
func configDocProblems(vars []config.Var, text string) []string {
	var problems []string
	known := map[string]bool{}
	for _, v := range vars {
		known[v.Name] = true
		if !namesWord(text, v.Name) {
			problems = append(problems, fmt.Sprintf("%s does not document %s", configDocPath, v.Name))
		}
	}
	// Settings the page invents.
	for _, m := range settingWord.FindAllStringSubmatch(text, -1) {
		if !known[m[2]] {
			problems = append(problems, fmt.Sprintf("%s documents %s, which the code does not read", configDocPath, m[2]))
		}
	}
	if !strings.Contains(text, config.ProfileRule) {
		problems = append(problems, fmt.Sprintf("%s does not say a profile name is %q", configDocPath, config.ProfileRule))
	}
	return problems
}

// settingWord is a GCAL_ name standing as a word of its own.
var settingWord = regexp.MustCompile(`(^|[^A-Z0-9_])(GCAL_[A-Z0-9_]+)`)

// namesWord reports whether text names the variable as a whole word, so
// a longer name that contains it, such as GCAL_READ_ONLY_X, does not
// count as GCAL_READ_ONLY.
func namesWord(text, name string) bool {
	return regexp.MustCompile(`(^|[^A-Z0-9_])` + regexp.QuoteMeta(name) + `($|[^A-Z0-9_])`).MatchString(text)
}

// pathRe finds anything shaped like a repository path in prose.
//
// Expect the naive version of this to be mostly false positives and
// budget for the triage: a sibling's first pass reported 13 broken
// references and every one was the extractor's fault. The exclusions
// below are that triage, not a widening.
// pathRe matches a repository path in backticks: a file under one of the
// source directories, or a root file recognized by its SHAPE.
//
// Shape, never existence. The obvious "derive the roots from the
// repository's own top-level entries" is circular and was tried here: a
// token is treated as a path only if its root exists, so a file that
// does not exist is filed as prose and excuses itself. That is the exact
// case this widening was for — `.goreleaser.yaml` was named in the docs
// while no such file existed, and §16 called the release built for four
// phases.
//
// So the root half is an extension list, kept to the extensions a
// repository file actually has at the top level. `.txt` and `.json` are
// deliberately absent: `checksums.txt` and `manifest.json` are named in
// the docs and live inside a release archive and a bundle, not here.
var pathRe = regexp.MustCompile(
	"`((?:cmd|internal|scripts|docs|testdata|packaging|\\.github)/[A-Za-z0-9_./-]+" +
		"|[A-Za-z0-9_.-]+\\.(?:md|ya?ml|toml|mod|sum)" +
		"|Makefile|LICENSE)`")

// pathExists resolves a documented path.
//
// A bare workflow name is also tried under .github/workflows, because
// the docs name `ci.yml` the way a person says it. It is still a
// reference that has to resolve: a doc naming a workflow nobody wrote
// fails here rather than reading as prose.
func pathExists(p string) bool {
	if _, err := os.Stat(p); err == nil {
		return true
	}
	if strings.Contains(p, "/") {
		return false
	}
	if ext := filepath.Ext(p); ext != ".yml" && ext != ".yaml" {
		return false
	}
	_, err := os.Stat(filepath.Join(".github", "workflows", p))
	return err == nil
}

// plannedRe marks a path a later phase builds: `internal/plan/` (phase 2).
var plannedRe = regexp.MustCompile("^`[^`]+`[,]? \\(phase [0-9]+\\)")

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
		text := string(data)
		for _, m := range pathRe.FindAllStringSubmatchIndex(text, -1) {
			p := strings.TrimSuffix(text[m[2]:m[3]], "/")
			key := doc + "|" + p
			if seen[key] {
				continue
			}
			seen[key] = true
			checked++
			if pathExists(p) {
				continue
			}
			// A path that does not exist yet is fine if the document
			// says which phase owes it. Without this the gate cannot
			// tell a plan from a stale reference, and §5 legitimately
			// describes packages later phases build.
			//
			// It stays strict in the direction that matters: the marker
			// has to name a phase, and it has to sit immediately after
			// the path, so "this will exist one day" is not enough.
			if plannedRe.MatchString(text[m[0]:]) {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s names %s, which does not exist and is not marked with the phase that builds it", doc, p))
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
