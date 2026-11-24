package main

import (
	"cmp"
	"io"
	"strings"
	"testing"
)

const (
	clHead      = "# Changelog\n\n## [Unreleased]\n\n"
	clReleased  = "## [0.9.0] - 2026-01-01\n\n### Added\n\n- The first tool.\n"
	clOneEntry  = clHead + "### Added\n\n- A second tool.\n\n"
	clNext      = "## [0.9.1] - 2026-02-01\n\n### Added\n\n- A second tool.\n\n"
	clNextEmpty = "## [0.9.1] - 2026-02-01\n\n"
)

// changelogRepo is a module whose binary imports internal/a and embeds a
// file, with a CHANGELOG, committed on main in the working directory.
func changelogRepo(t *testing.T) (gomod string, git func(args ...string)) {
	t.Helper()
	t.Setenv("GOWORK", "off")
	dir, git := isolatedRepo(t)
	gomod = "module example.com/fixture\n\ngo 1.21\n\nrequire example.com/dep v1.0.0\n\nreplace example.com/dep => ./dep\n"
	for rel, body := range map[string]string{
		"CHANGELOG.md":          clHead + clReleased,
		"go.mod":                gomod,
		"dep/go.mod":            "module example.com/dep\n\ngo 1.21\n",
		"dep/dep.go":            "package dep\n",
		"cmd/fixture/main.go":   "package main\n\nimport (\n\t_ \"example.com/dep\"\n\t_ \"example.com/fixture/internal/a\"\n)\n\nfunc main() {}\n",
		"internal/a/a.go":       "package a\n\nimport _ \"embed\"\n\n//go:embed data/x.txt\nvar X string\n",
		"internal/a/data/x.txt": "x\n",
		"docs/x.md":             "x\n",
	} {
		put(t, dir, rel, body)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "first")
	return gomod, git
}

// A pull request that changes what ships adds an entry under
// [Unreleased]. One that cuts a release, bumps a required module, ships
// nothing, or carries a `Changelog: none` footer with a reason needs none.
// A bump of the MCP SDK needs one.
func TestTheChangelogGateWantsAnEntryPerPullRequest(t *testing.T) {
	const sdk = "require github.com/modelcontextprotocol/go-sdk v1.8.0\n"
	code := map[string]string{"internal/a/b.go": "package a\n\n// changed\n"}
	with := func(files map[string]string, changelog string) map[string]string {
		out := map[string]string{"CHANGELOG.md": changelog}
		for k, v := range files {
			out[k] = v
		}
		return out
	}
	for _, tc := range []struct {
		name string
		// prep is committed on main before the topic branches off.
		prep, files map[string]string
		message     string
		refused     bool
		// first, when set, is an earlier commit's message on the topic.
		first string
	}{
		{name: "shipped code with an entry", files: with(code, clOneEntry+clReleased)},
		{name: "shipped code with no entry", files: code, refused: true},
		{name: "an embedded file with no entry", files: map[string]string{"internal/a/data/x.txt": "y\n"}, refused: true},
		{name: "the bundle's manifest with no entry", files: map[string]string{changelogManifest: "{}\n"}, refused: true},
		{name: "a shipped file whose name git quotes", files: map[string]string{"internal/a/naïve notes.md": "x\n"}, refused: true},
		{name: "shipped code with an entry under a released heading",
			files: with(code, clHead+strings.Replace(clReleased, "- The first tool.\n", "- The first tool.\n- Late.\n", 1)), refused: true},
		{name: "shipped code with an edited entry", prep: map[string]string{"CHANGELOG.md": clOneEntry + clReleased},
			files: with(code, strings.Replace(clOneEntry, "A second tool.", "A second tool, renamed.", 1)+clReleased), refused: true},
		{name: "a release cut", prep: map[string]string{"CHANGELOG.md": clOneEntry + clReleased},
			files: map[string]string{"CHANGELOG.md": clHead + clNext + clReleased, "internal/a/b.go": "package a\n"}},
		{name: "a version heading with no entries under it", files: with(code, clHead+clNextEmpty+clReleased), refused: true},
		{name: "an entry under a new version heading, [Unreleased] empty before", files: with(code, clHead+clNext+clReleased), refused: true},
		{name: "a new package the binary imports, with no entry", files: map[string]string{
			"internal/b/b.go":     "package b\n",
			"cmd/fixture/main.go": "package main\n\nimport (\n\t_ \"example.com/dep\"\n\t_ \"example.com/fixture/internal/a\"\n\t_ \"example.com/fixture/internal/b\"\n)\n\nfunc main() {}\n",
		}, refused: true},
		{name: "the bundle's launcher with no entry", files: map[string]string{launcherPath: "#!/bin/sh\n"}, refused: true},
		{name: "the release build with no entry", files: map[string]string{".goreleaser.yaml": "version: 2\n"}, refused: true},
		{name: "a Changelog: None footer in another case", files: code, message: "refactor\n\nChangelog: None, a rename"},
		{name: "a Changelog footer whose word only starts with none", files: code,
			message: "change\n\nChangelog: nonetheless user-visible", refused: true},
		{name: "a refactor with a Changelog: none footer", files: code,
			message: "refactor\n\nChangelog: none, the text is unchanged line for line"},
		{name: "a Changelog: none footer with no reason", files: code, message: "refactor\n\nChangelog: none", refused: true},
		{name: "a footer whose word ends with a period", files: code, message: "refactor\n\nChangelog: none. internal rename only"},
		{name: "a footer whose word runs on with an underscore", files: code,
			message: "change\n\nChangelog: none_yet, user-visible fix pending", refused: true},
		{name: "a footer in the reference form", files: code, message: "refactor\n\nChangelog #none, a rename", refused: true},
		{name: "a footer beside an entry under a released heading", message: "refactor\n\nChangelog: none, a rename",
			files: with(code, clHead+strings.Replace(clReleased, "- The first tool.\n", "- The first tool.\n- Late.\n", 1)), refused: true},
		{name: "a bump of the MCP SDK with a footer", prep: map[string]string{"go.mod": "SDK"}, message: "refactor\n\nChangelog: none, a rename",
			files: map[string]string{"go.mod": "SDK-BUMP"}, refused: true},
		{name: "a footer only in an earlier commit", files: code, message: "refactor, part two",
			first: "refactor\n\nChangelog: none, a rename", refused: true},
		{name: "a dependency bump", files: map[string]string{"go.mod": "BUMP"}},
		{name: "a go line change", files: map[string]string{"go.mod": "GO"}, refused: true},
		{name: "a bump of the MCP SDK", prep: map[string]string{"go.mod": "SDK"}, files: map[string]string{"go.mod": "SDK-BUMP"}, refused: true},
		{name: "nothing shipped", files: map[string]string{"docs/x.md": "y\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gomod, git := changelogRepo(t)
			goMods := map[string]string{
				"BUMP":     strings.Replace(gomod, "dep v1.0.0", "dep v1.1.0", 1),
				"GO":       strings.Replace(gomod, "go 1.21", "go 1.22", 1),
				"SDK":      gomod + sdk,
				"SDK-BUMP": gomod + strings.Replace(sdk, "v1.8.0", "v1.9.0", 1),
			}
			commit := func(files map[string]string, message string) {
				for rel, body := range files {
					if rel == "go.mod" {
						body = goMods[body]
					}
					put(t, ".", rel, body)
				}
				git("add", "-A")
				git("commit", "-q", "-m", message)
			}
			if tc.prep != nil {
				commit(tc.prep, "prep")
			}
			git("checkout", "-q", "-b", "topic")
			if tc.first != "" {
				git("commit", "-q", "--allow-empty", "-m", tc.first)
			}
			commit(tc.files, cmp.Or(tc.message, "change"))
			// The base branch moves on with shipped code and its own entry;
			// measured from the merge-base, neither is the topic's.
			git("checkout", "-q", "main")
			commit(map[string]string{"internal/a/c.go": "package a\n",
				"CHANGELOG.md": clHead + "- Main's own.\n\n" + clReleased}, "main moved")
			// The gate reads the binary's packages from the checkout, which
			// CI has at the head.
			git("checkout", "-q", "topic")
			err := changelogGate(io.Discard, "main", "topic")
			switch {
			case tc.refused && (err == nil || exitCode(err) != 1):
				t.Errorf("changelogGate = %v; want a refusal", err)
			case !tc.refused && err != nil:
				t.Errorf("changelogGate = %v; want a pass", err)
			}
		})
	}
}

// A require of a noted module is read on its own line and in a block, with
// a comment after it, and nothing else is.
func TestGoModNotedRequires(t *testing.T) {
	const gomod = "module m\n\nrequire github.com/modelcontextprotocol/go-sdk v1.8.0 // direct\n\n" +
		"require (\n\texample.com/a v1.0.0\n\tgithub.com/modelcontextprotocol/go-sdk v1.9.0\n)\n"
	got := strings.Join(goModNotedRequires(gomod), "|")
	if want := "github.com/modelcontextprotocol/go-sdk v1.8.0|github.com/modelcontextprotocol/go-sdk v1.9.0"; got != want {
		t.Errorf("goModNotedRequires = %q, want %q", got, want)
	}
}

// An empty `require ()` opens no block, so a replace after it is still
// read; `require(` and `require (` each open one.
func TestGoModBuildLinesReadsPastAnEmptyBlock(t *testing.T) {
	const base = "module m\n\nrequire ()\n\nreplace example.com/a => ./a\n"
	if goModBuildLines(base) == goModBuildLines(strings.Replace(base, "./a", "./b", 1)) {
		t.Error("a replace after `require ()` was swallowed")
	}
	for _, opener := range []string{"require (", "require(", "require ( // direct"} {
		gomod := "module m\n\n" + opener + "\n\texample.com/a v1.0.0\n)\n"
		if goModBuildLines(gomod) != goModBuildLines(strings.Replace(gomod, "v1.0.0", "v1.1.0", 1)) {
			t.Errorf("a bump inside `%s` counted as a build change", opener)
		}
	}
}
