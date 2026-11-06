package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"maps"
	"math/bits"
	"os"
	"regexp"
	"slices"
	"strings"
)

// ciWorkflowPath is the workflow `make check` is held to, and
// publishWorkflowPath the release workflow that publishes the registry
// entry.
const (
	ciWorkflowPath      = ".github/workflows/ci.yml"
	publishWorkflowPath = ".github/workflows/publish-mcp.yml"
)

// minTargets is the fewest make targets `make check` and ci.yml may each
// name. Fewer means the gate has stopped reading one of them.
const minTargets = 8

// parityGate asserts that `make check` and CI run the same things, and
// that each gates command runs in the pipeline main.go says it does.
//
// The two lists live in different files and you are only ever editing
// one of them. They drift apart, usually with the local one ahead, and
// then the build-tagged files compile only on a maintainer's laptop.
//
// A comment in the Makefile claiming "everything CI runs" is the
// sentence that stops the next person checking, so this checks.
func parityGate() error {
	makefile, err := os.ReadFile(makefilePath)
	if err != nil {
		return err
	}
	ci, err := os.ReadFile(ciWorkflowPath)
	if err != nil {
		return err
	}
	var release []string
	for _, path := range []string{releaseWorkflowPath, publishWorkflowPath} {
		flow, err := os.ReadFile(path) //nolint:gosec // a repository path from a constant
		if err != nil {
			return err
		}
		release = append(release, runText(string(flow)))
	}
	hooks, err := os.ReadFile(goreleaserPath)
	if err != nil {
		return err
	}
	release = append(release, uncommented(string(hooks)))
	files, err := checkoutFiles()
	if err != nil {
		return couldNotRun{err}
	}
	tags, err := treeBuildTags(files)
	if err != nil {
		return err
	}
	problems, summary, err := parityProblems(string(makefile), string(ci), strings.Join(release, "\n"), commands, tags)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %s\n", summary)
	return nil
}

// parityProblems is the rule over the text of the Makefile, ci.yml and the
// release's files, the commands and the tree's build tags, so a test can
// hand it files that disagree. CI runs make targets, so the two lists are
// compared by target name.
func parityProblems(makefile, ci, release string, registry map[string]command, tags []string) (problems []string, summary string, err error) {
	rules := parseMakefile(makefile)
	check, ok := rules["check"]
	if !ok {
		return nil, "", fmt.Errorf("no `check:` target in the Makefile")
	}
	targets := slices.Sorted(slices.Values(check.prereqs))
	run := runText(ci)
	ciSteps := map[string]bool{}
	for _, m := range makeRe.FindAllStringSubmatch(run, -1) {
		ciSteps[m[1]] = true
	}

	if len(targets) < minTargets {
		return nil, "", fmt.Errorf("`make check` names only %d targets; that is not the gate set", len(targets))
	}
	if len(ciSteps) < minTargets {
		return nil, "", fmt.Errorf("ci.yml runs only %d make targets; that is not the gate set", len(ciSteps))
	}
	if len(tags) == 0 {
		return nil, "", fmt.Errorf("no //go:build line requires a tag; the tree is not being read")
	}

	for _, t := range targets {
		if !ciSteps[t] {
			problems = append(problems, fmt.Sprintf("`make check` runs %q and CI does not", t))
		}
	}
	ciTargets := slices.Sorted(maps.Keys(ciSteps))
	for _, t := range ciTargets {
		if !slices.Contains(targets, t) && !runsPRGate(registry, recipesOf(rules, []string{t})) {
			problems = append(problems, fmt.Sprintf("CI runs `make %s` and `make check` does not", t))
		}
	}

	// A gate is spelled $(GATES) in a recipe, so the gates are read before
	// the variables are expanded.
	localRecipes, ciRecipes := recipesOf(rules, []string{"check"}), recipesOf(rules, ciTargets)
	pipelines, held := pipelineProblems(registry, gatesIn(localRecipes), gatesIn(run+"\n"+ciRecipes), gatesIn(release))
	problems = append(problems, pipelines...)

	vars := makeVars(makefile)
	local, remote := expand(localRecipes, vars), run+"\n"+expand(ciRecipes, vars)
	problems = append(problems, vetProblems(tags, local, remote)...)
	problems = append(problems, goRunProblems(run, local)...)

	summary = fmt.Sprintf("%d targets in `make check`, all run by CI; %d commands run where scripts/gates says; "+
		"a vet pass for -tags=%s on both sides", len(targets), held, strings.Join(tags, ", -tags="))
	return problems, summary, nil
}

// pipelineProblems holds each command to the pipeline it declares: a check
// gate to `make check` and CI, a pull-request gate to CI alone, and a
// release step to the release's files. Each argument after the registry is
// the gates commands one side runs. held is how many commands it held.
func pipelineProblems(registry map[string]command, local, remote, release map[string]bool) (problems []string, held int) {
	for _, name := range sortedKeys(registry) {
		switch registry[name].runsIn {
		case manual:
			continue
		case inCheck:
			if !local[name] {
				problems = append(problems, fmt.Sprintf("%q runs in make check and no `make check` prerequisite runs it", name))
			}
			if !remote[name] {
				problems = append(problems, fmt.Sprintf("%q runs in make check and ci.yml does not run it", name))
			}
		case inPR:
			if !remote[name] {
				problems = append(problems, fmt.Sprintf("%q runs on a pull request and ci.yml does not run it", name))
			}
			if local[name] {
				problems = append(problems, fmt.Sprintf("%q runs on a pull request and `make check` runs it, "+
					"where there is no merge-base to measure from", name))
			}
		case inRelease:
			if !release[name] {
				problems = append(problems, fmt.Sprintf("%q runs in the release and none of %s, %s and %s runs it; "+
					"a step dropped from them fails at a tag, in public", name, releaseWorkflowPath, publishWorkflowPath, goreleaserPath))
			}
		default:
			problems = append(problems, fmt.Sprintf("%q declares no pipeline in scripts/gates/main.go", name))
			continue
		}
		held++
	}
	return problems, held
}

// runsPRGate reports whether a recipe runs a pull-request gate, which CI
// runs and `make check` cannot.
func runsPRGate(registry map[string]command, recipe string) bool {
	for name := range gatesIn(recipe) {
		if registry[name].runsIn == inPR {
			return true
		}
	}
	return false
}

// gatesIn is every gates command a text runs, by name.
func gatesIn(text string) map[string]bool {
	in := map[string]bool{}
	for _, m := range gatesCommand.FindAllStringSubmatch(text, -1) {
		in[m[1]] = true
	}
	return in
}

// vetProblems holds each -tags list the tree requires to a vet pass on
// both sides that sets all of it: a file behind build tags compiles only
// in a pass with those tags. A side's text is its run lines and the
// recipes of the targets it runs.
func vetProblems(tags []string, local, remote string) []string {
	var problems []string
	localPasses, remotePasses := vetPasses(local), vetPasses(remote)
	for _, tag := range tags {
		if !vetted(localPasses, tag) {
			problems = append(problems, fmt.Sprintf(
				"a file builds only with -tags=%s and `make check` vets no such pass; a contributor cannot see it break", tag))
		}
		if !vetted(remotePasses, tag) {
			problems = append(problems, fmt.Sprintf(
				"a file builds only with -tags=%s and ci.yml vets no such pass; it compiles only on a maintainer's machine", tag))
		}
	}
	return problems
}

// vetted reports whether one vet pass sets every tag of a -tags list.
func vetted(passes [][]string, tags string) bool {
	return slices.ContainsFunc(passes, func(pass []string) bool {
		return !slices.ContainsFunc(strings.Split(tags, ","), func(tag string) bool { return !slices.Contains(pass, tag) })
	})
}

// goRunProblems is the other direction for a tool CI runs itself rather
// than through make: `make check` runs the same module at the same
// version, or a contributor cannot reproduce what CI ran.
func goRunProblems(run, local string) []string {
	var problems []string
	ran := map[string]bool{}
	for _, m := range goRunAt.FindAllStringSubmatch(local, -1) {
		ran[m[1]+"@"+m[2]] = true
	}
	for _, m := range goRunAt.FindAllStringSubmatch(run, -1) {
		if module := m[1] + "@" + m[2]; !ran[module] {
			problems = append(problems, fmt.Sprintf(
				"ci.yml runs `go run %s` and no `make check` prerequisite runs it; a contributor cannot reproduce it", module))
		}
	}
	return problems
}

var (
	makeRe = regexp.MustCompile(`make\s+([a-z][a-z0-9-]*)`)
	// makeRule is a rule line: targets, a colon, and not the := of an
	// assignment.
	makeRule = regexp.MustCompile(`(?m)^([^\s:#=]+(?:[ \t]+[^\s:#=]+)*)[ \t]*:(?:[^=]|$)`)
	// vetCommand is a vet command and its arguments, up to a shell
	// separator.
	vetCommand = regexp.MustCompile(`\bvet\b([^;&|\n]*)`)
	// goRunAt is a module `go run` runs, and the version it runs.
	goRunAt = regexp.MustCompile(`\bgo run\s+([^\s@]+)@(\S+)`)
	// makeAssign is a `NAME = value` line, with its operator: =, :=, ?=
	// or +=, after any export or override.
	makeAssign = regexp.MustCompile(`(?m)^(?:(?:export|override)[ \t]+)*([A-Z_][A-Z0-9_]*)[ \t]*([:?+]?=)[ \t]*(.*)$`)
	makeRef    = regexp.MustCompile(`\$\([A-Za-z_][A-Za-z0-9_]*\)`)
)

// maxExpandDepth is how many levels of variables expand follows, enough
// for assignments that refer to each other a few levels deep.
const maxExpandDepth = 5

// vetPasses is the tags of each vet command in text that sets any, from
// its -tags flag wherever it sits among the arguments: -tags=a,b or
// -tags a,b, with one dash or two (https://pkg.go.dev/flag#hdr-Command_line_flag_syntax).
func vetPasses(text string) [][]string {
	var passes [][]string
	for _, m := range vetCommand.FindAllStringSubmatch(text, -1) {
		args := strings.Fields(m[1])
		for i, arg := range args {
			name, value, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			if !strings.HasPrefix(arg, "-") || name != "tags" {
				continue
			}
			if !inline && i+1 < len(args) {
				value = args[i+1]
			}
			if value = strings.Trim(value, `"'`); value != "" {
				passes = append(passes, strings.Split(value, ","))
			}
		}
	}
	return passes
}

// recipesOf is the recipe of each target and of every target it depends
// on, once each, as make runs them.
func recipesOf(rules map[string]makeTarget, targets []string) string {
	var out []string
	seen := map[string]bool{}
	var visit func(target string)
	visit = func(target string) {
		if seen[target] {
			return
		}
		seen[target] = true
		for _, dep := range rules[target].prereqs {
			visit(dep)
		}
		for _, c := range rules[target].recipe {
			out = append(out, c.text)
		}
	}
	for _, target := range targets {
		visit(target)
	}
	return strings.Join(out, "\n")
}

// makeTarget is one Makefile rule: what it depends on and what it runs.
type makeTarget struct {
	prereqs []string
	recipe  []makeCommand
}

// makeCommand is one command of a recipe and the Makefile line it starts
// on. Its variables are as written: a caller expands what it compares.
type makeCommand struct {
	line int
	text string
}

// parseMakefile reads a Makefile, its comment lines blank, into its rules
// by target. A rule line is makeRule's, so `a b: deps` gives both targets
// the rule and `check:=` is an assignment. The prerequisites leave out the
// trailing `## help text`. As in make, a target's rules merge their
// prerequisites and the last recipe is the one that runs
// (https://www.gnu.org/software/make/manual/html_node/Multiple-Rules.html).
func parseMakefile(makefile string) map[string]makeTarget {
	lines := strings.Split(uncommented(makefile), "\n")
	rules := map[string]makeTarget{}
	for i, line := range lines {
		m := makeRule.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		_, after, _ := strings.Cut(line[m[3]:], ":")
		deps, _, _ := strings.Cut(after, "##")
		recipe := readRecipe(lines[i+1:], i+2)
		for _, target := range strings.Fields(line[m[2]:m[3]]) {
			rule := rules[target]
			rule.prereqs = slices.Clone(rule.prereqs)
			for _, dep := range strings.Fields(deps) {
				if !slices.Contains(rule.prereqs, dep) {
					rule.prereqs = append(rule.prereqs, dep)
				}
			}
			if len(recipe) > 0 {
				rule.recipe = recipe
			}
			rules[target] = rule
		}
	}
	return rules
}

// makeVars reads the Makefile's variable assignments as make does: a #
// starts a comment, a later `=` or `:=` replaces a value, `?=` sets only a
// variable not yet set, and `+=` appends after a space
// (https://www.gnu.org/software/make/manual/html_node/Setting.html,
// https://www.gnu.org/software/make/manual/html_node/Appending.html).
func makeVars(makefile string) map[string]string {
	vars := map[string]string{}
	for _, m := range makeAssign.FindAllStringSubmatch(makefile, -1) {
		old, set := vars[m[1]]
		value, _, _ := strings.Cut(m[3], "#")
		value = strings.TrimSpace(value)
		switch {
		case m[2] == "?=" && set:
			continue
		case m[2] == "+=" && old != "":
			value = strings.TrimSpace(old + " " + value)
		}
		vars[m[1]] = value
	}
	return vars
}

// makeVariable is one variable makeVars read, so each path and version
// the Makefile pins has a single owner.
func makeVariable(vars map[string]string, name string) (string, error) {
	if value := vars[name]; value != "" {
		return value, nil
	}
	return "", fmt.Errorf("%s defines no %s", makefilePath, name)
}

// expand substitutes $(NAME) until nothing changes, so a recipe spelled
// `$(GO) run $(GOVULNCHECK)` compares with a workflow that spells the same
// command literally. A $$ is make's escape for one $ handed to the shell,
// so it is kept out of the substitution and unescaped last.
func expand(line string, vars map[string]string) string {
	line = strings.ReplaceAll(line, "$$", "\x00")
	for range maxExpandDepth {
		next := makeRef.ReplaceAllStringFunc(line, func(ref string) string {
			if v, ok := vars[ref[2:len(ref)-1]]; ok {
				return v
			}
			return ref
		})
		if next == line {
			break
		}
		line = next
	}
	return strings.ReplaceAll(line, "\x00", "$")
}

// readRecipe is the recipe of the rule whose line precedes lines, one
// command each, with a line a backslash continues joined to the next.
// first is the Makefile line lines starts at. A recipe line starts with a
// tab, and blank lines among them are ignored
// (https://www.gnu.org/software/make/manual/html_node/Recipe-Syntax.html).
func readRecipe(lines []string, first int) []makeCommand {
	var recipe []makeCommand
	joining := false
	for i, l := range lines {
		body, isRecipe := strings.CutPrefix(l, "\t")
		if !isRecipe {
			if strings.TrimSpace(l) == "" {
				continue
			}
			break
		}
		body = strings.TrimSpace(body)
		if !joining {
			recipe = append(recipe, makeCommand{line: first + i})
			body = strings.TrimLeft(body, "@-+")
		}
		body, joining = strings.CutSuffix(body, `\`)
		recipe[len(recipe)-1].text += " " + strings.TrimSpace(body)
	}
	for i := range recipe {
		recipe[i].text = strings.TrimSpace(recipe[i].text)
	}
	return slices.DeleteFunc(recipe, func(c makeCommand) bool { return c.text == "" })
}

// uncommented is a file with its comment lines blank, so its line numbers
// are still the file's. A comment is text that does not run, and a recipe
// line commented out to unblock a red build would otherwise read as one
// that runs.
func uncommented(file string) string {
	lines := strings.Split(file, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
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

// treeBuildTags is every -tags list a //go:build line in the checkout
// requires, each once and sorted: a file that builds with the list and not
// without it, which an untagged vet never compiles.
func treeBuildTags(files []string) ([]string, error) {
	fset := token.NewFileSet()
	var tags []string
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") || inTestdata(rel) {
			continue
		}
		f, err := parser.ParseFile(fset, rel, nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return nil, err
		}
		tags = append(tags, requiredTags(f)...)
	}
	slices.Sort(tags)
	return slices.Compact(tags), nil
}

// goBuildExpr is a file's //go:build expression, or nil when it has none
// or the line does not parse.
// https://pkg.go.dev/cmd/go#hdr-Build_constraints
func goBuildExpr(f *ast.File) constraint.Expr {
	for _, group := range f.Comments {
		if group.Pos() >= f.Package {
			break
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return nil
			}
			return expr
		}
	}
	return nil
}

// requiredTags is each -tags list a file's //go:build line needs: none
// when it builds with no custom tag, and otherwise each smallest set of
// custom tags it builds with, joined by commas. A tag the go command sets
// for a target or a toolchain is free to be either value, so `e2e && linux`
// needs e2e, and `a && b` needs one pass that sets both.
func requiredTags(f *ast.File) []string {
	expr := goBuildExpr(f)
	if expr == nil {
		return nil
	}
	var free, custom []string
	// Eval calls its function for every tag, even past a decided result.
	expr.Eval(func(tag string) bool {
		switch {
		case tag == "ignore" || slices.Contains(free, tag) || slices.Contains(custom, tag):
		case goToolchainTag(tag):
			free = append(free, tag)
		default:
			custom = append(custom, tag)
		}
		return false
	})
	slices.Sort(custom)
	if buildsWith(expr, free, nil) {
		return nil
	}
	for size := 1; size <= len(custom); size++ {
		var lists []string
		for set := range 1 << len(custom) {
			if bits.OnesCount(uint(set)) != size {
				continue
			}
			var with []string
			for i, tag := range custom {
				if set&(1<<i) != 0 {
					with = append(with, tag)
				}
			}
			if buildsWith(expr, free, with) {
				lists = append(lists, strings.Join(with, ","))
			}
		}
		if lists != nil {
			return lists
		}
	}
	return nil
}

// buildsWith reports whether a file builds with the custom tags in with,
// and no other, for some value of each free tag.
func buildsWith(expr constraint.Expr, free, with []string) bool {
	for set := range 1 << len(free) {
		if expr.Eval(func(tag string) bool {
			if i := slices.Index(free, tag); i >= 0 {
				return set&(1<<i) != 0
			}
			return slices.Contains(with, tag)
		}) {
			return true
		}
	}
	return false
}

// goKnownOS and goKnownArch are the GOOS and GOARCH values go/build
// knows, as https://go.dev/src/internal/syslist/syslist.go lists them.
var (
	goKnownOS = map[string]bool{
		"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true,
		"hurd": true, "illumos": true, "ios": true, "js": true, "linux": true, "nacl": true,
		"netbsd": true, "openbsd": true, "plan9": true, "solaris": true, "wasip1": true,
		"windows": true, "zos": true,
	}
	goKnownArch = map[string]bool{
		"386": true, "amd64": true, "amd64p32": true, "arm": true, "armbe": true, "arm64": true,
		"arm64be": true, "loong64": true, "mips": true, "mipsle": true, "mips64": true,
		"mips64le": true, "mips64p32": true, "mips64p32le": true, "ppc": true, "ppc64": true,
		"ppc64le": true, "riscv": true, "riscv64": true, "s390": true, "s390x": true,
		"sparc": true, "sparc64": true, "wasm": true,
	}
	// goOtherTags are set by the go command for a target and a toolchain,
	// except ignore, which keeps a file out of every build and is never set.
	goOtherTags = map[string]bool{"unix": true, "cgo": true, "gc": true, "gccgo": true, "ignore": true}
	// goReleaseTag is a release tag, such as go1.22, set for each release
	// up to the toolchain's own.
	goReleaseTag = regexp.MustCompile(`^go1\.\d+$`)
)

// goToolchainTag reports whether a build tag is one no -tags flag needs
// to name: an OS, an architecture or one of its features, such as
// amd64.v2, or a tag the go command sets.
// https://pkg.go.dev/cmd/go#hdr-Build_constraints
func goToolchainTag(tag string) bool {
	arch, _, _ := strings.Cut(tag, ".")
	return goKnownOS[tag] || goKnownArch[arch] || goOtherTags[tag] || goReleaseTag.MatchString(tag)
}
