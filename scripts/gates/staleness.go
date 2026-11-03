package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v5/internal/auth"
	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
)

// staleness holds the documentation against the code.
//
// Its scope is deliberately narrow and named, because the failure these
// gates describe applies to the checker too: a
// staleness gate whose scope is a hand-maintained list is the same
// problem one level up, passing while claims outside its list go wrong.
//
// What it holds:
//
//  1. every tool the binary publishes appears in the README's table,
//     every tool named there exists, and every Claude Code permission
//     rule the README gives names a tool the binary publishes;
//  2. every GCAL_* variable the server reads is documented by name, a
//     removed one included, no other is, and the configuration page
//     states the profile rule;
//  3. no version number is written in prose in the README — a badge
//     cannot go stale, a copy of a fact can;
//  4. docs/gcp-setup.md's Read-only and Read-write blocks each list,
//     one to a line, exactly the scopes login asks for in that mode, and
//     no scope line sits outside them.
//
// The paths the docs name are the docs gate's to hold.
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

	problems = append(problems, checkNoVersionInProse()...)

	scopeProblems, scopes, err := checkSetupScopes()
	if err != nil {
		return err
	}
	problems = append(problems, scopeProblems...)

	// Floors.
	if tools < 5 {
		return fmt.Errorf("the binary published %d tools; that is not the surface", tools)
	}
	if settings < 8 {
		return fmt.Errorf("found %d settings in the code; that is not the configuration surface", settings)
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d tools, %d settings, %d setup scopes: all current\n", tools, settings, scopes)
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
	documented := map[string]bool{}
	for _, name := range settingNames(text) {
		documented[name] = true
	}
	known := map[string]bool{}
	for _, v := range vars {
		known[v.Name] = true
		if !documented[v.Name] {
			problems = append(problems, fmt.Sprintf("%s does not document %s", configDocPath, v.Name))
		}
	}
	// Settings the page invents.
	for _, name := range slices.Sorted(maps.Keys(documented)) {
		if !known[name] {
			problems = append(problems, fmt.Sprintf("%s documents %s, which the code does not read", configDocPath, name))
		}
	}
	if !strings.Contains(text, config.ProfileRule) {
		problems = append(problems, fmt.Sprintf("%s does not say a profile name is %q", configDocPath, config.ProfileRule))
	}
	return problems
}

// settingWord is a setting's name standing as a word of its own, all of
// it: a letter of either case, a digit or _ on either side makes it part
// of a longer word, so GCAL_READ_ONLY_X, GCAL_READ_ONLYs and
// xGCAL_READ_ONLY are not GCAL_READ_ONLY. A name ends in a capital or a
// digit, so GCAL_LIVE_ is a prefix rather than a name.
var settingWord = regexp.MustCompile(`\b` + config.EnvPrefix + `[A-Z0-9_]*[A-Z0-9]\b`)

// settingNames is every variable text names, each a whole word. One
// followed by * is a pattern, such as GCAL_LIVE*, and not a variable,
// unless a * comes before it too, as Markdown's bold and italics do.
func settingNames(text string) []string {
	var names []string
	for _, at := range settingWord.FindAllStringIndex(text, -1) {
		pattern := strings.HasPrefix(text[at[1]:], "*") && !strings.HasSuffix(text[:at[0]], "*")
		if !pattern {
			names = append(names, text[at[0]:at[1]])
		}
	}
	return names
}

// versionInProseRe catches a version written into the README's text.
//
// A badge shows the version, updates itself and cannot be wrong. A
// number in prose is a copy of a fact, and a copy goes stale. The fix is
// to delete the copy, not to build a gate that keeps it correct.
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

// setupDocPath is the page a person pastes the consent screen's scopes
// from. A scope missing there is a 403 from one tool weeks after a
// working setup, on an External app; a scope listed there that login
// never asks for is one a person grants for nothing.
const setupDocPath = "docs/gcp-setup.md"

// setupBlocks are the scope blocks setupDocPath carries, each a fenced
// block under a heading that starts with these words, and the access it
// is for. Sharing is on in both; the page says in prose to drop the acls
// scopes with it off, which checkSetupScopes holds against the code.
var setupBlocks = []struct {
	heading string
	access  auth.Access
}{
	{"Read-only", auth.Access{ReadOnly: true, Sharing: true}},
	{"Read-write", auth.Access{Sharing: true}},
}

// checkSetupScopes holds setupDocPath's scope blocks against auth.Scopes.
func checkSetupScopes() ([]string, int, error) {
	raw, err := os.ReadFile(setupDocPath)
	if err != nil {
		return nil, 0, fmt.Errorf("reading %s: %w", setupDocPath, err)
	}
	problems := setupScopeProblems(string(raw))
	n := 0
	for _, b := range setupBlocks {
		on := auth.Scopes(b.access)
		n += len(on)
		// The page's sharing-off rule: the same block, less its acls
		// scopes, and nothing else.
		var wantOff []string
		for _, s := range on {
			if !strings.Contains(s, ".acls") {
				wantOff = append(wantOff, s)
			}
		}
		off := b.access
		off.Sharing = false
		got := auth.Scopes(off)
		if len(auth.MissingScopes(got, wantOff)) > 0 || len(auth.MissingScopes(wantOff, got)) > 0 {
			problems = append(problems, fmt.Sprintf("%s says to drop the acls scopes with %sSHARING=off, and login's %s set without sharing is %v",
				setupDocPath, config.EnvPrefix, b.heading, got))
		}
	}
	return problems, n, nil
}

// setupScopeProblems compares each of text's scope blocks with what login
// asks for under its access, both ways, and refuses a scope line that
// sits in neither block.
func setupScopeProblems(text string) []string {
	blocks := map[string][]string{}
	var stray []string
	heading := ""
	raw := strings.Split(text, "\n")
	// prose blanks every line of a fence, its markers included, so this
	// gate and the docs gate agree on what is code.
	for i, outside := range prose(text) {
		line := strings.TrimSpace(raw[i])
		fenced := outside == "" && line != ""
		switch {
		case !fenced && strings.HasPrefix(line, "#"):
			heading = strings.TrimSpace(strings.TrimLeft(line, "#"))
		case strings.HasPrefix(line, "https://www.googleapis.com/auth/"):
			block := ""
			for _, b := range setupBlocks {
				if strings.HasPrefix(heading, b.heading) {
					block = b.heading
				}
			}
			if fenced && block != "" {
				blocks[block] = append(blocks[block], line)
			} else {
				stray = append(stray, line)
			}
		}
	}
	var problems []string
	for _, b := range setupBlocks {
		got, ok := blocks[b.heading]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s has no %s scope block", setupDocPath, b.heading))
			continue
		}
		want := auth.Scopes(b.access)
		for _, s := range auth.MissingScopes(got, want) {
			problems = append(problems, fmt.Sprintf("%s: the %s block does not list %s, which login asks for there", setupDocPath, b.heading, s))
		}
		for _, s := range auth.MissingScopes(want, got) {
			problems = append(problems, fmt.Sprintf("%s: the %s block lists %s, which login does not ask for there", setupDocPath, b.heading, s))
		}
	}
	for _, s := range stray {
		problems = append(problems, fmt.Sprintf("%s lists %s outside the scope blocks", setupDocPath, s))
	}
	return problems
}
