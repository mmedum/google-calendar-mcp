package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
)

// The standard says: break the manifest all six ways and watch each be
// refused before you believe the check. A check nobody has watched fail
// is a check nobody knows the shape of — doing this in a sibling
// repository found a packer that verified the entry point and the Linux
// files and never the win32 override's command, so a typo in the .exe
// path would have packed cleanly, installed cleanly, and been caught by
// nothing.
//
// Each case below breaks exactly one thing in a manifest that is
// otherwise correct, and asserts the sentence that names it.

// repoFile reads a path relative to the repository root.
//
// The gates run from the root, which they chdir to themselves; a test
// runs from its own package directory. One helper rather than the same
// fallback written at each call site.
func repoFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// repoPath resolves a repository-relative path from wherever the test is
// running, for the callers that need the PATH rather than the bytes.
func repoPath(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat("../../" + path); err == nil {
		return "../../" + path
	}
	return path
}

// good is the committed manifest, which every case starts from.
func good(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(repoFile(t, manifestPath), &m); err != nil {
		t.Fatalf("the committed manifest is not valid JSON: %v", err)
	}
	return m
}

// The committed manifest passes every check the gate and the packer
// run, read from the repository root. The one pass of it here: every
// other test breaks it.
func TestTheCommittedManifestIsValid(t *testing.T) {
	_, _, problems, err := committedManifest(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("the committed manifest fails the gate:\n%s", strings.Join(problems, "\n"))
	}
}

func TestTheSixWaysAManifestBreaks(t *testing.T) {
	overrides := []string{"server", "mcp_config", "platform_overrides"}
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{
			name: "an entry point nobody stages",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "server/google-calendar-mcp-mac" },
			want: `entry_point "server/google-calendar-mcp-mac" is not a program the packer stages`,
		},
		{
			name: "no entry point at all",
			edit: func(d map[string]any) { delete(dig(d, "server"), "entry_point") },
			want: "the manifest declares no entry_point",
		},
		{
			// A staged program, and not the one the default command runs:
			// the bundle names the Windows binary as the server and spawns
			// the macOS one.
			name: "an entry point the default command does not run",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "server/google-calendar-mcp.exe" },
			want: `entry_point is "server/google-calendar-mcp.exe", and mcp_config.command runs ` +
				`"server/google-calendar-mcp-darwin"`,
		},
		{
			// Packed, and not a program: the license is there to be read.
			name: "an entry point naming the license",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "LICENSE" },
			want: `entry_point "LICENSE" is not a program the packer stages`,
		},
		{
			name: "a platform command nobody stages",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["command"] = "${__dirname}/server/google-calendar-mcp-windows.exe"
			},
			want: `platform_overrides.win32.command is "${__dirname}/server/google-calendar-mcp-windows.exe", ` +
				"which is not a program the packer stages",
		},
		{
			name: "a command outside the bundle",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config")["command"] = "/usr/local/bin/google-calendar-mcp"
			},
			want: `mcp_config.command is "/usr/local/bin/google-calendar-mcp"; a command starts with ${__dirname}/`,
		},
		{
			// The specification shows this form once and never says what
			// it is resolved against (§18 row 112).
			name: "a relative command",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config")["command"] = "server/google-calendar-mcp-darwin"
			},
			want: `mcp_config.command is "server/google-calendar-mcp-darwin"; a command starts with ${__dirname}/`,
		},
		{
			// 4.1.0 stripped the leading slash and read this as staged.
			name: "an override's absolute command",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["command"] = "/server/google-calendar-mcp.exe"
			},
			want: `platform_overrides.win32.command is "/server/google-calendar-mcp.exe"; a command starts with ${__dirname}/`,
		},
		{
			name: "a command naming the README",
			edit: func(d map[string]any) { dig(d, "server", "mcp_config")["command"] = "${__dirname}/README.md" },
			want: `mcp_config.command is "${__dirname}/README.md", which is not a program the packer stages`,
		},
		{
			name: "an override naming the license",
			edit: func(d map[string]any) { dig(d, append(overrides, "linux")...)["command"] = "${__dirname}/LICENSE" },
			want: `platform_overrides.linux.command is "${__dirname}/LICENSE", which is not a program the packer stages`,
		},
		{
			// Staged, and a program, and the file staged for another
			// platform: macOS would spawn a Windows binary. entry_point
			// follows the command, so that only this is wrong.
			name: "macOS spawning the Windows binary",
			edit: func(d map[string]any) {
				dig(d, "server")["entry_point"] = "server/google-calendar-mcp.exe"
				dig(d, "server", "mcp_config")["command"] = "${__dirname}/server/google-calendar-mcp.exe"
			},
			want: `platform "darwin" runs "server/google-calendar-mcp.exe", and the file staged for it is ` +
				`"server/google-calendar-mcp-darwin"`,
		},
		{
			name: "Windows spawning the macOS binary",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["command"] = "${__dirname}/server/google-calendar-mcp-darwin"
			},
			want: `platform "win32" runs "server/google-calendar-mcp-darwin", and the file staged for it is ` +
				`"server/google-calendar-mcp.exe"`,
		},
		{
			// Deleting the override passes every check above: win32 then
			// runs the default command, which is the macOS universal
			// binary, and that file really is in the bundle.
			name: "no win32 override",
			edit: func(d map[string]any) { delete(dig(d, overrides...), "win32") },
			want: `platform "win32" runs "server/google-calendar-mcp-darwin"`,
		},
		{
			name: "no linux override",
			edit: func(d map[string]any) { delete(dig(d, overrides...), "linux") },
			want: `platform "linux" runs "server/google-calendar-mcp-darwin", and the file staged for it is ` +
				`"server/launch-linux.sh"`,
		},
		{
			// Right for one of the two architectures Claude Desktop for
			// Linux ships.
			name: "one Linux binary for both architectures",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "linux")...)["command"] = "${__dirname}/server/google-calendar-mcp-linux-amd64"
			},
			want: `platform "linux" runs "server/google-calendar-mcp-linux-amd64", and the file staged for it is ` +
				`"server/launch-linux.sh"`,
		},
		{
			name: "an override for a platform the bundle does not claim",
			edit: func(d map[string]any) {
				dig(d, overrides...)["freebsd"] = map[string]any{"command": "${__dirname}/server/launch-linux.sh"}
			},
			want: `platform_overrides has "freebsd", which compatibility.platforms does not claim`,
		},
		{
			// Composed, not the whole value: a check that only looked at
			// values that ARE a reference would miss this one and the
			// server would start with ${user_config.dir} unsubstituted.
			name: "an env value spending a key nobody declared",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config", "env")["GCAL_CONFIG_DIR"] = "${user_config.dir}/google-calendar-mcp"
			},
			want: "mcp_config.env.GCAL_CONFIG_DIR spends ${user_config.dir}, which user_config does not declare",
		},
		{
			name: "an argument spending a key nobody declared",
			edit: func(d map[string]any) { dig(d, "server", "mcp_config")["args"] = []any{"${user_config.verbose}"} },
			want: "mcp_config.args spends ${user_config.verbose}, which user_config does not declare",
		},
		{
			name: "an override's argument spending a key nobody declared",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["args"] = []any{"-profile=${user_config.account}"}
			},
			want: "platform_overrides.win32.args spends ${user_config.account}, which user_config does not declare",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := validateManifest(edited(t, c.edit), bundleFiles)
			if len(problems) != 1 || !strings.Contains(problems[0], c.want) {
				t.Fatalf("problems:\n%s\nwant the one containing %q", strings.Join(problems, "\n"), c.want)
			}
		})
	}
}

// copiedRoot is a repository holding the files the gate and the packer
// read, copied from this one, so that one can be broken.
func copiedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{manifestPath, launcherPath, "go.mod", "LICENSE", "README.md"} {
		put(t, root, rel, string(repoFile(t, rel)))
	}
	return root
}

// put writes body to rel under root.
func put(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// committedManifest runs every check the gate has, on the files under
// the root it is given, not the working directory: a check broken in a
// copy of the repository is refused, and refused alone. The checks the
// packer's table breaks are not broken again here.
func TestManifestProblemsRunsEveryCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		// file is the one broken; edit applies to the manifest, and
		// old and new to any other file.
		file     string
		edit     func(map[string]any)
		old, new string
		want     string
	}{
		{name: "a version in the tree", file: manifestPath, edit: func(d map[string]any) { d["version"] = "1.2.3" },
			want: `the committed manifest says version "1.2.3"`},
		{name: "a schema at a branch", file: manifestPath, edit: func(d map[string]any) {
			d["$schema"] = "https://raw.githubusercontent.com/anthropics/mcpb/main/schemas/mcpb-manifest-v0.3.schema.json"
		}, want: "can be re-pointed"},
		{name: "a document the schema refuses", file: manifestPath, edit: func(d map[string]any) { d["keywords"] = "calendar" },
			want: "the committed manifest does not satisfy mcpb-manifest-v0.3.schema.json"},
		// Every check before identityProblems passed this: no platform
		// was left to spawn the wrong file.
		{name: "Linux dropped with its override", file: manifestPath, edit: func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32"}
			delete(dig(d, "server", "mcp_config", "platform_overrides"), "linux")
		}, want: "compatibility.platforms is [darwin win32]; the packer stages entry points for [darwin linux win32]"},
		// The 4.0.0 name: the family's layout is -linux-amd64.
		{name: "a launcher of other names", file: launcherPath,
			old: "google-calendar-mcp-linux-amd64", new: "google-calendar-mcp-linux-x64",
			want: "the launcher runs [google-calendar-mcp-linux-arm64 google-calendar-mcp-linux-x64]; " +
				"the packer stages [google-calendar-mcp-linux-amd64 google-calendar-mcp-linux-arm64]"},
		// The decode into manifest merges the two, so every check sees
		// the env and the overrides; the schema and the host keep the
		// second, which has neither.
		{name: "a second server object", file: manifestPath, old: `  "user_config": {`,
			new: `  "server": {"type": "binary", "entry_point": "server/google-calendar-mcp-darwin", ` +
				`"mcp_config": {"command": "${__dirname}/server/google-calendar-mcp-darwin", "args": []}},` +
				"\n" + `  "user_config": {`,
			want: "the manifest has server more than once"},
		{name: "a variable mapped twice", file: manifestPath, old: `"GCAL_PROFILE": "${user_config.profile}",`,
			new:  `"GCAL_PROFILE": "${user_config.profile}", "GCAL_PROFILE": "${user_config.profile}",`,
			want: "the manifest has server.mcp_config.env.GCAL_PROFILE more than once"},
		// The install hint is held to go.mod, which moves at every major
		// version.
		{name: "a go.mod at another major version", file: "go.mod", old: "/v5", new: "/v4",
			want: `the launcher says "go install github.com/mmedum/google-calendar-mcp/v5/cmd/google-calendar-mcp@latest", ` +
				`and go.mod's module gives "go install github.com/mmedum/google-calendar-mcp/v4/cmd/google-calendar-mcp@latest"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := copiedRoot(t)
			if tc.edit != nil {
				put(t, root, tc.file, string(editedRaw(t, tc.edit)))
			} else {
				body := string(repoFile(t, tc.file))
				if !strings.Contains(body, tc.old) {
					t.Fatalf("%s does not say %q", tc.file, tc.old)
				}
				put(t, root, tc.file, strings.Replace(body, tc.old, tc.new, 1))
			}
			_, _, problems, err := committedManifest(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Errorf("problems:\n%s\nwant the one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// A key is repeated only within one object, and each repeated key is
// named once, by its path, a list's elements and a repeated object's
// own keys included.
func TestRepeatedKeysNameEachPathOnce(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want []string
	}{
		{`{"a": [{"b": 1, "b": 2, "b": 3}], "c": {"d": 1}, "d": 1}`, []string{"a[0].b"}},
		{`{"x": {"y": 1}, "x": {"y": 1, "y": 2}}`, []string{"x", "x.y"}},
		{`{"server": {"type": "binary"}, "user_config": {"type": {"type": "boolean"}}}`, nil},
		{`{"a": 1,`, nil},
	} {
		if got := repeatedKeys([]byte(tc.doc)); !slices.Equal(got, tc.want) {
			t.Errorf("repeatedKeys(%s) = %q, want %q", tc.doc, got, tc.want)
		}
	}
}

// TestTheLauncherWritesItsRefusalToStderr: a line of English on stdout
// corrupts the JSON-RPC session before the client's first request
// completes, so an unknown architecture has to fail the other way.
func TestTheLauncherWritesItsRefusalToStderr(t *testing.T) {
	body := launcherSource(t)
	if !strings.Contains(body, ">&2") {
		t.Fatal("the launcher does not write its refusal to stderr")
	}
	if !strings.Contains(body, "exit 1") {
		t.Fatal("the launcher does not exit non-zero on an unknown architecture")
	}
	// exec, not a call: the server talks MCP over this process's stdio,
	// and a shell left in the middle owns the pipes.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "google-calendar-mcp-linux") && !strings.Contains(line, "exec ") {
			t.Fatalf("the launcher runs a binary without exec: %q", strings.TrimSpace(line))
		}
	}
}

// TestPackRefusesThePlaceholderAsAVersion: the packer writes the real
// version, and a build that passed the placeholder through would ship a
// bundle claiming 0.0.0-dev.
func TestPackRefusesThePlaceholderAsAVersion(t *testing.T) {
	err := packMCPB(t.TempDir(), placeholderVersion, t.TempDir()+"/out.mcpb")
	if err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("packing with the placeholder version gave %v", err)
	}
}

func mentions(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

func launcherSource(t *testing.T) string {
	t.Helper()
	return string(repoFile(t, launcherPath))
}

// TestTheManifestIsTheJSONThePackerWrites: the version goes in through a
// decode and an encode, so a manifest cannot be corrupted by a
// substitution over text.
func TestTheManifestIsTheJSONThePackerWrites(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(repoFile(t, manifestPath), &document); err != nil {
		t.Fatalf("the committed manifest is not valid JSON: %v", err)
	}
	document["version"] = "1.2.3"
	out, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"version": "1.2.3"`) {
		t.Fatal("the stamped manifest does not carry the version")
	}
}

// The ways a manifest can misdeclare its own version, and the missing
// support URL. This check did not exist while three of the seven
// servers drifted apart — one carried no $schema, and two declared
// manifest_version 0.2 while pointing at the UNPINNED schema path, which
// serves whatever upstream publishes today. Two of those were written
// from the third as a template, which is how a stale shape spreads.
func TestTheWaysAManifestMisdeclaresItsVersion(t *testing.T) {
	const pinned = "https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.3.schema.json"

	good := manifest{Schema: pinned, ManifestVersion: "0.3", Support: "https://example.invalid/issues"}
	if problems := checkManifestShape(good); len(problems) > 0 {
		t.Fatalf("a well-formed manifest was refused:\n%s", strings.Join(problems, "\n"))
	}

	// A commit SHA is the other ref that cannot move, and the stronger
	// of the two. Refusing it would push somebody back to a branch.
	bySHA := good
	bySHA.Schema = "https://raw.githubusercontent.com/anthropics/mcpb/" +
		"0123456789abcdef0123456789abcdef01234567/schemas/mcpb-manifest-v0.3.schema.json"
	if problems := checkManifestShape(bySHA); len(problems) > 0 {
		t.Fatalf("a schema pinned to a commit was refused:\n%s", strings.Join(problems, "\n"))
	}

	cases := []struct {
		name string
		m    manifest
		want string
	}{
		{
			name: "no $schema at all",
			m:    manifest{ManifestVersion: "0.3", Support: "x"},
			want: "no $schema",
		},
		{
			// The defect that actually shipped: conformance claimed
			// against one version, validated against whatever dist/ holds.
			name: "the unpinned schema path",
			m: manifest{
				Schema:          "https://raw.githubusercontent.com/anthropics/mcpb/main/dist/mcpb-manifest.schema.json",
				ManifestVersion: "0.2", Support: "x",
			},
			want: "not the pinned",
		},
		{
			name: "a pinned schema that disagrees with manifest_version",
			m:    manifest{Schema: pinned, ManifestVersion: "0.2", Support: "x"},
			want: "cannot claim one version and validate against another",
		},
		{
			name: "no support URL",
			m:    manifest{Schema: pinned, ManifestVersion: "0.3"},
			want: "no support URL",
		},
		{
			// The version pins the format; the ref pins the bytes. A
			// branch can be amended under a document claiming to
			// conform to it, which is the residue of the same defect
			// one level down.
			name: "a schema served from a branch",
			m: manifest{
				Schema:          "https://raw.githubusercontent.com/anthropics/mcpb/main/schemas/mcpb-manifest-v0.3.schema.json",
				ManifestVersion: "0.3", Support: "x",
			},
			want: "can be re-pointed",
		},
		{
			// The case a blacklist of branch names passes: a tag that
			// upstream moves as it releases. It reads as pinned.
			name: "a partial tag",
			m: manifest{
				Schema:          "https://raw.githubusercontent.com/anthropics/mcpb/v2.1/schemas/mcpb-manifest-v0.3.schema.json",
				ManifestVersion: "0.3", Support: "x",
			},
			want: "can be re-pointed",
		},
		{
			// And the case both of those pass: the right filename,
			// served by somebody else.
			name: "the right file from another host",
			m: manifest{
				Schema:          "https://example.invalid/schemas/mcpb-manifest-v0.3.schema.json",
				ManifestVersion: "0.3", Support: "x",
			},
			want: "not upstream's published path",
		},
		{
			// The case the other claims cannot see: 0.2 with a 0.2
			// schema is wrong and entirely self-consistent, so a
			// sibling's stale manifest pasted in passes every check
			// that holds the document against itself.
			name: "a self-consistent manifest a version behind",
			m: manifest{
				Schema:          "https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.2.schema.json",
				ManifestVersion: "0.2", Support: "x",
			},
			want: "still a manifest a version behind",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := checkManifestShape(tc.m)
			if len(problems) == 0 {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !mentions(problems, tc.want) {
				t.Fatalf("wanted %q, got:\n%s", tc.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// The floor compares numbers, not strings. "0.10" sorts before "0.3" as
// text, which is a bug that waits until the tenth minor version to
// appear and then looks like the manifest's fault.
func TestTheFloorComparesVersionsAsNumbers(t *testing.T) {
	for _, c := range []struct {
		version, floor string
		want           bool
	}{
		{"0.3", "0.3", false},
		{"0.2", "0.3", true},
		{"0.4", "0.3", false},
		{"0.10", "0.3", false},
		{"1.0", "0.3", false},
		{"0.9", "1.0", true},
		{"nonsense", "0.3", true},
	} {
		if got := behind(c.version, c.floor); got != c.want {
			t.Errorf("behind(%q, %q) = %v, want %v", c.version, c.floor, got, c.want)
		}
	}
}

// editedRaw is the committed manifest with one edit applied to its
// decoded document, encoded again.
func editedRaw(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(repoFile(t, manifestPath), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// edited is the committed manifest with one edit applied, read back the
// way the gate reads it.
func edited(t *testing.T, edit func(map[string]any)) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(editedRaw(t, edit), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// dig is the object at a path in a decoded manifest.
func dig(doc map[string]any, path ...string) map[string]any {
	for _, key := range path {
		doc = doc[key].(map[string]any)
	}
	return doc
}

// Each rule the bundle's settings are held to, broken once in the
// committed manifest.
func TestTheBundleSettingsRefuseEachBreak(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"a switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
		}, "the bundle's defaults configure the server otherwise than no environment does, in EnableDestructive;"},
		{"a required switch the bundle turns on, which Claude Desktop fills in", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
			dig(d, "user_config", "enable_destructive")["required"] = true
		}, "the bundle's defaults configure the server otherwise than no environment does, in EnableDestructive;"},
		{"a switch the bundle turns off", func(d map[string]any) {
			dig(d, "user_config", "sharing")["default"] = false
		}, "the bundle's defaults configure the server otherwise than no environment does, in Sharing;"},
		{"two defaults that configure something", func(d map[string]any) {
			dig(d, "user_config", "read_only")["default"] = true
			dig(d, "user_config", "profile")["default"] = "work"
		}, "the bundle's defaults configure the server otherwise than no environment does, in Profile, ReadOnly;"},
		{"a default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "profile")["default"] = "Not A Profile"
		}, "the environment the bundle's defaults give the server is refused: config: invalid: profile"},
		{"a field with a list for a default", func(d map[string]any) {
			dig(d, "user_config", "client_secret")["default"] = []any{"client.json"}
		}, "user_config.client_secret defaults to a list, which the defaults check cannot build an environment from"},
		{"a switch asked as text", func(d map[string]any) {
			dig(d, "user_config", "sharing")["type"] = "string"
			dig(d, "user_config", "sharing")["default"] = "on"
		}, "user_config.sharing is a string; GCAL_SHARING is a switch, so ask for a boolean"},
		{"a switch with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "enable_decline_all"), "default")
		}, `the environment the bundle's defaults give the server is refused: config: invalid: enable-decline-all ` +
			`"${user_config.enable_decline_all}"`},
		{"an optional key with no default, as 4.0.0 shipped the client file", func(d map[string]any) {
			delete(dig(d, "user_config", "client_secret"), "default")
		}, "user_config.client_secret is optional with no default"},
		{"an override's env, which replaces the base", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "win32")["env"] = map[string]any{
				"GCAL_ENABLE_DESTRUCTIVE": "true",
			}
		}, "platform_overrides.win32.env is set"},
		{"an empty override env, which replaces the base too", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "linux")["env"] = map[string]any{}
		}, "platform_overrides.linux.env is set"},
		{"no argument list in mcp_config", func(d map[string]any) {
			delete(dig(d, "server", "mcp_config"), "args")
		}, "mcp_config.args is missing; keep it as []"},
		{"a null argument list in mcp_config", func(d map[string]any) {
			dig(d, "server", "mcp_config")["args"] = nil
		}, "mcp_config.args is null; keep it as []"},
		{"an argument that turns the deletes on", func(d map[string]any) {
			dig(d, "server", "mcp_config")["args"] = []any{"-enable-destructive=true"}
		}, `mcp_config.args is ["-enable-destructive=true"]`},
		{"an override's argument, which replaces the base", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "linux")["args"] = []any{"-enable-destructive=true"}
		}, "platform_overrides.linux.args is set"},
		{"an empty override argument list, which replaces the base too", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "win32")["args"] = []any{}
		}, "platform_overrides.win32.args is set"},
		{"a setting the bundle no longer asks for", func(d map[string]any) {
			delete(dig(d, "user_config"), "enable_decline_all")
			delete(dig(d, "server", "mcp_config", "env"), "GCAL_ENABLE_DECLINE_ALL")
		}, "user_config does not ask for enable_decline_all, which sets GCAL_ENABLE_DECLINE_ALL"},
		{"a setting spent under another key", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GCAL_PROFILE"] = "${user_config.sharing}"
		}, `mcp_config.env.GCAL_PROFILE is "${user_config.sharing}", want "${user_config.profile}"`},
		{"a key no setting has", func(d map[string]any) {
			dig(d, "user_config")["verbose"] = map[string]any{
				"type": "boolean", "title": "Verbose", "description": "d", "default": false}
		}, "user_config asks for verbose, which is no setting's key"},
		{"a setting the bundle leaves out", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GCAL_LOG_LEVEL"] = "debug"
		}, "mcp_config.env sets GCAL_LOG_LEVEL, which the bundle leaves out: diagnostics"},
		{"the read-only switch's removed name", func(d map[string]any) {
			env := dig(d, "server", "mcp_config", "env")
			env["GCAL_READONLY"] = env["GCAL_READ_ONLY"]
			delete(env, "GCAL_READ_ONLY")
		}, "mcp_config.env sets GCAL_READONLY, which a release removed"},
		{"a removed setting", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GCAL_REQUIRE_PROMPT"] = "false"
		}, "mcp_config.env sets GCAL_REQUIRE_PROMPT, which a release removed"},
		{"a variable the server does not read", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GCAL_SHARE"] = "${user_config.sharing}"
		}, "mcp_config.env sets GCAL_SHARE, which the server does not read"},
		{"a profile field that does not say what a name is", func(d map[string]any) {
			dig(d, "user_config", "profile")["description"] = "Which stored login to use. Lower case, digits, - and _."
		}, `user_config.profile does not say a profile name is "up to 64 characters from a to z, 0 to 9, - and _, ` +
			`starting with a letter or digit"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problems := settingProblems(edited(t, tc.edit), bundleLeftOut); !mentions(problems, tc.want) {
				t.Errorf("problems:\n%s\nwant one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// What the bundle says it is, broken once each: the server's own name, a
// binary, the family's floor, a privacy policy, and exactly the
// platforms the packer stages an entry point for.
func TestTheIdentityRefusesEachBreak(t *testing.T) {
	const entryPoints = "; the packer stages entry points for [darwin linux win32]"
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"another name", func(d map[string]any) { d["name"] = "google-calendar" },
			`the manifest's name is "google-calendar" and the server's is "google-calendar-mcp"`},
		{"a node server", func(d map[string]any) { dig(d, "server")["type"] = "node" },
			`server.type is "node"; the bundle carries a binary`},
		{"no Claude Desktop floor", func(d map[string]any) { delete(dig(d, "compatibility"), "claude_desktop") },
			`compatibility.claude_desktop is ""; the family's bundles claim ">=0.10.0"`},
		{"no Linux", func(d map[string]any) { dig(d, "compatibility")["platforms"] = []any{"darwin", "win32"} },
			"compatibility.platforms is [darwin win32]" + entryPoints},
		{"a platform the packer stages nothing for", func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32", "linux", "freebsd"}
		}, "compatibility.platforms is [darwin freebsd linux win32]" + entryPoints},
		{"a platform claimed twice", func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32", "linux", "linux"}
		}, "compatibility.platforms is [darwin linux linux win32]" + entryPoints},
		{"no privacy policy", func(d map[string]any) { delete(d, "privacy_policies") }, "privacy_policies is empty"},
		{"an empty privacy policy list", func(d map[string]any) { d["privacy_policies"] = []any{} },
			"privacy_policies is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problems := identityProblems(edited(t, tc.edit), bundleFiles); len(problems) != 1 ||
				!strings.HasPrefix(problems[0], tc.want) {
				t.Errorf("problems:\n%s\nwant the one starting %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
	// The platforms come from what is staged, not from a list of its own.
	files := slices.DeleteFunc(slices.Clone(bundleFiles), func(f staged) bool { return f.platform == "linux" })
	want := []string{"compatibility.platforms is [darwin linux win32]; the packer stages entry points for [darwin win32]"}
	if got := identityProblems(good(t), files); !slices.Equal(got, want) {
		t.Errorf("nothing staged for Linux gives %q, want %q", got, want)
	}
}

// Every default the check cannot read is reported, in key order, and
// the comparison still runs over the rest, so one run names everything
// wrong with the defaults.
func TestDefaultsProblemsReportsEveryUnreadableDefaultAndTheRest(t *testing.T) {
	m := edited(t, func(d map[string]any) {
		uc := dig(d, "user_config")
		dig(uc, "profile")["default"] = map[string]any{"name": "work"}
		dig(uc, "client_secret")["default"] = []any{"client.json"}
		dig(uc, "enable_destructive")["default"] = true
	})
	const cannot = ", which the defaults check cannot build an environment from: give it a boolean, a string or a number"
	want := []string{
		"user_config.client_secret defaults to a list" + cannot,
		"user_config.profile defaults to an object" + cannot,
	}
	got, err := defaultsProblems(m)
	if err != nil || len(got) != 3 || !slices.Equal(got[:2], want) ||
		!strings.HasPrefix(got[2], "the bundle's defaults configure the server otherwise than no environment does, in EnableDestructive;") {
		t.Errorf("defaultsProblems = %v,\n%s\nwant\n%s\nand then the comparison", err, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The bundle asks for every variable the server reads, less the removed
// ones and less each one left out with a reason, so a setting added
// later is a decision about the bundle rather than a gap in it. An
// exclusion the server has no use for is refused.
func TestBundleVarsAreDerivedFromTheSettings(t *testing.T) {
	asked, problems := bundleVarsFrom(
		[]config.Var{{Name: "GCAL_ASKED"}, {Name: "GCAL_LEFT_OUT"}, {Name: "GCAL_REMOVED", Removed: true},
			{Name: "GCAL_ADDED_LATER", Switch: true}},
		map[string]string{"GCAL_LEFT_OUT": "a reason", "GCAL_GONE": "a reason", "GCAL_REMOVED": "a reason"},
	)
	if want := []config.Var{{Name: "GCAL_ASKED"}, {Name: "GCAL_ADDED_LATER", Switch: true}}; !slices.Equal(asked, want) {
		t.Errorf("asked = %v, want %v", asked, want)
	}
	want := []string{
		"bundleLeftOut names GCAL_GONE, which the server does not read",
		"bundleLeftOut names GCAL_REMOVED, which a release removed, and so left out already",
	}
	if !slices.Equal(problems, want) {
		t.Errorf("problems = %q, want %q", problems, want)
	}
}

// A stale exclusion fails the gate itself, not only the derivation, and
// a variable added later is asked for until the bundle asks or a reason
// is written.
func TestSettingProblemsRefuseAStaleExclusion(t *testing.T) {
	leftOut := maps.Clone(bundleLeftOut)
	leftOut["GCAL_GONE"] = "a reason"
	want := "bundleLeftOut names GCAL_GONE, which the server does not read"
	if got := settingProblems(good(t), leftOut); !slices.Contains(got, want) {
		t.Errorf("settingProblems = %q, want %q among them", got, want)
	}
	leftOut = maps.Clone(bundleLeftOut)
	delete(leftOut, "GCAL_WRITE_TIMEOUT")
	want = "user_config does not ask for write_timeout, which sets GCAL_WRITE_TIMEOUT"
	if got := settingProblems(good(t), leftOut); !slices.Contains(got, want) {
		t.Errorf("settingProblems = %q, want %q among them", got, want)
	}
}

// The profile field is held to what the server accepts. A bundle that
// drops the field is told so once, and one that leaves the setting out
// is refused, so the field check cannot pass in silence.
func TestTheFieldRulesHaveAFloor(t *testing.T) {
	dropped := edited(t, func(d map[string]any) {
		delete(dig(d, "user_config"), "profile")
		delete(dig(d, "server", "mcp_config", "env"), "GCAL_PROFILE")
	})
	if p := slices.DeleteFunc(settingProblems(dropped, bundleLeftOut), func(p string) bool {
		return !strings.Contains(p, "GCAL_PROFILE")
	}); !slices.Equal(p, []string{"user_config does not ask for profile, which sets GCAL_PROFILE"}) {
		t.Errorf("a dropped profile field gives %q, want the one problem", p)
	}
	leftOut := maps.Clone(bundleLeftOut)
	leftOut["GCAL_PROFILE"] = "left out"
	want := fmt.Sprintf("the bundle asks for no GCAL_PROFILE, so nothing holds its field to %q",
		"up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit")
	if got := settingProblems(dropped, leftOut); !slices.Contains(got, want) {
		t.Errorf("a bundle with no GCAL_PROFILE gives %q, want %q among them", got, want)
	}
}

// A field the defaults change is named, an unexported one included
// where its type compares; an unexported one that does not is reported
// unnamed, rather than panicking.
func TestTheConfigDiffNamesTheFieldsThatDiffer(t *testing.T) {
	type cfg struct {
		Shown  int
		hidden int
		rest   []string
		held   any
	}
	for _, tc := range []struct {
		b    cfg
		want []string
	}{
		{cfg{2, 1, nil, nil}, []string{"Shown"}},
		{cfg{1, 2, nil, nil}, []string{"hidden"}},
		{cfg{1, 1, []string{"x"}, nil}, []string{"an unexported field"}},
		{cfg{1, 1, nil, nil}, nil},
	} {
		if got := fieldDiff(cfg{1, 1, nil, nil}, tc.b); !slices.Equal(got, tc.want) {
			t.Errorf("fieldDiff against %+v = %q, want %q", tc.b, got, tc.want)
		}
	}
	// An interface holding a slice on both sides compares as a type and
	// panics in Value.Equal; it is reported unnamed instead.
	if got := fieldDiff(cfg{held: []string{"y"}}, cfg{held: []string{"x"}}); !slices.Equal(got,
		[]string{"an unexported field"}) {
		t.Errorf("an interface field differing gives %q, want [an unexported field]", got)
	}
	if got := configDiff(config.Config{Profile: "a", Sharing: true}, config.Config{Profile: "a"}); !slices.Equal(got,
		[]string{"Sharing"}) {
		t.Errorf("configDiff = %q, want [Sharing]", got)
	}
}

// bundleSwitches is the switches the committed bundle asks for.
var bundleSwitches = []string{"enable_decline_all", "enable_destructive", "read_only", "sharing"}

// refusingReadOnly is the committed manifest with GCAL_REQUIRE_PROMPT
// spending read_only, so the server refuses read_only on, and nothing
// else. Build refuses no set of the switches, and no setting it reads
// can be made to refuse a pair, so this is how the refusal check is made
// to fire.
func refusingReadOnly(t *testing.T, edit func(map[string]any)) manifest {
	t.Helper()
	return edited(t, func(d map[string]any) {
		dig(d, "server", "mcp_config", "env")["GCAL_REQUIRE_PROMPT"] = "${user_config.read_only}"
		edit(d)
	})
}

// refusals is refusalProblems over the bundle's switches, the defaults
// checked first as settingProblems checks them.
func refusals(t *testing.T, m manifest, declared [][]string) []string {
	t.Helper()
	_, defaultsErr := defaultsProblems(m)
	return refusalProblems(m, bundleSwitches, declared, defaultsErr)
}

// undeclaredReadOnly is the problem refusingReadOnly gives with nothing
// declared.
const undeclaredReadOnly = "the server refuses [read_only on], and bundleRefused does not declare it"

// Every set of the switches is started, and the refused ones are held to
// the declared list both ways, each defect reported once, for the
// smallest set that shows it.
func TestTheBundleRefusalsAreDeclared(t *testing.T) {
	committed := good(t)
	if got := refusals(t, refusingReadOnly(t, func(map[string]any) {}), nil); len(got) != 1 ||
		!strings.HasPrefix(got[0], undeclaredReadOnly) {
		t.Errorf("an undeclared refusal gives %q, want the one problem %q", got, undeclaredReadOnly)
	}
	declared := [][]string{{"read_only"}}
	if got := refusals(t, refusingReadOnly(t, func(map[string]any) {}), declared); len(got) != 0 {
		t.Errorf("a declared refusal gives %q, want nothing", got)
	}
	const starts = "bundleRefused declares that the server refuses [read_only on], and it starts with them"
	if got := refusals(t, committed, declared); !slices.Equal(got, []string{starts}) {
		t.Errorf("a declared refusal the server starts with gives %q, want %q once", got, starts)
	}
	// When the defaults do not start, no set is started: the defaults
	// check reports it.
	if got := refusalProblems(committed, bundleSwitches, declared,
		errors.New("the defaults do not start")); len(got) != 0 {
		t.Errorf("a set was started beside defaults that do not start: %q", got)
	}
	if got := refusals(t, committed, [][]string{{"profile", "read_only"}}); !slices.Contains(got,
		"bundleRefused names profile, which is not a switch the bundle asks for") {
		t.Errorf("a declared key that is not a switch gives %q", got)
	}
}

// Each field in a declared set names the others' titles, trimmed, and a
// key with no title is reported once, however many sets name it.
func TestTheDeclaredSetsNameEachOther(t *testing.T) {
	pair := [][]string{{"enable_destructive", "read_only"}}
	named := edited(t, func(d map[string]any) {
		dig(d, "user_config", "read_only")["description"] = "Refused with Deleting on."
		dig(d, "user_config", "enable_destructive")["description"] = "Refused with Read-only on."
		dig(d, "user_config", "enable_destructive")["title"] = " Deleting "
	})
	if got := declaredProblems(named, bundleSwitches, pair); len(got) != 0 {
		t.Errorf("fields that name each other give %q, want nothing", got)
	}
	unnamed := edited(t, func(d map[string]any) {
		dig(d, "user_config", "read_only")["description"] = "Register only the reads."
		dig(d, "user_config", "enable_destructive")["description"] = "Register the deletes."
	})
	want := []string{
		`user_config.enable_destructive does not name "Read-only", which the server refuses it with`,
		`user_config.read_only does not name "Deleting, publishing and sharing outside", which the server refuses it with`,
	}
	if got := declaredProblems(unnamed, bundleSwitches, pair); !slices.Equal(got, want) {
		t.Errorf("fields that do not name each other give %q, want %q", got, want)
	}
	untitled := edited(t, func(d map[string]any) { dig(d, "user_config", "read_only")["title"] = " " })
	twice := [][]string{{"enable_destructive", "read_only"}, {"read_only"}}
	for _, defaultsErr := range []error{nil, errors.New("the defaults do not start")} {
		got := refusalProblems(untitled, bundleSwitches, twice, defaultsErr)
		if n := len(slices.DeleteFunc(got, func(p string) bool {
			return !strings.HasPrefix(p, "user_config.read_only has no title")
		})); n != 1 {
			t.Errorf("defaults error %v: an untitled read_only is reported %d times, want 1", defaultsErr, n)
		}
	}
}

// A switch in a set is on unless its default is true: a required one
// with no default, or one with a list, is turned on rather than left
// out, which would hide a refusal.
func TestTheRefusalsTurnASwitchOnUnlessItDefaultsOn(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"required with no default": func(d map[string]any) {
			dig(d, "user_config", "read_only")["required"] = true
			delete(dig(d, "user_config", "read_only"), "default")
		},
		"a list default": func(d map[string]any) { dig(d, "user_config", "read_only")["default"] = []any{"true"} },
	} {
		got := refusals(t, refusingReadOnly(t, edit), nil)
		if !slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, undeclaredReadOnly) }) {
			t.Errorf("%s: got %q, want one starting %q", name, got, undeclaredReadOnly)
		}
	}
}

// A switch that defaults to true is started off, so a refusal that
// needs sharing off is found. The profile is made to depend on sharing:
// "true" is four letters and "false" five, so after 60 letters the name
// is 64 characters with sharing on, which the profile rule allows, and
// 65 with it off, which it refuses.
func TestTheRefusalsTurnSharingOff(t *testing.T) {
	m := edited(t, func(d map[string]any) {
		dig(d, "server", "mcp_config", "env")["GCAL_PROFILE"] = strings.Repeat("a", 60) + "${user_config.sharing}"
	})
	const want = "the server refuses [sharing off], and bundleRefused does not declare it"
	if got := refusals(t, m, nil); len(got) != 1 || !strings.HasPrefix(got[0], want) {
		t.Errorf("a refusal of sharing off gives %q, want the one problem %q", got, want)
	}
	if got := refusals(t, m, [][]string{{"sharing"}}); len(got) != 0 {
		t.Errorf("a declared refusal of sharing off gives %q, want nothing", got)
	}
}

// settingProblems runs the refusal check over the switches the bundle
// asks for, so a refusal Build adds fails the gate itself.
func TestSettingProblemsHoldTheRefusals(t *testing.T) {
	got := settingProblems(refusingReadOnly(t, func(map[string]any) {}), bundleLeftOut)
	if !slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, undeclaredReadOnly) }) {
		t.Errorf("settingProblems = %q, want one starting %q", got, undeclaredReadOnly)
	}
	// Defaults the server refuses start no set, which would each fail
	// with them: the defaults problem is the one report.
	got = settingProblems(edited(t, func(d map[string]any) {
		dig(d, "user_config", "profile")["default"] = "Not A Profile"
	}), bundleLeftOut)
	if len(got) != 1 || !strings.HasPrefix(got[0], "the environment the bundle's defaults give the server is refused") {
		t.Errorf("defaults the server refuses give %q, want that one problem", got)
	}
}

// The launcher's install hint names the module go.mod declares, which
// modulePath reads under Windows line endings too. The path moves with
// each major version, so a hint left behind sends somebody to an old
// one.
func TestTheLauncherInstallHintIsGoModsModule(t *testing.T) {
	const hint = `echo "no binary." "Install with: go install example.com/m/v4/cmd/google-calendar-mcp@latest" >&2`
	if problems := installHintProblems(hint, "example.com/m/v4"); len(problems) > 0 {
		t.Errorf("the hint for the module: %v", problems)
	}
	for _, tc := range []struct{ name, launcher, want string }{
		{"a hint without the major version", strings.Replace(hint, "/v4", "", 1),
			`the launcher says "go install example.com/m/cmd/google-calendar-mcp@latest"`},
		{"a hint a major version behind", strings.Replace(hint, "/v4", "/v3", 1),
			`the launcher says "go install example.com/m/v3/cmd/google-calendar-mcp@latest"`},
		{"no hint at all", "exit 1", "gives no install hint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := installHintProblems(tc.launcher, "example.com/m/v4"); !mentions(got, tc.want) {
				t.Errorf("problems %q, want %q", got, tc.want)
			}
		})
	}
	for _, tc := range []struct{ goMod, module, err string }{
		{"module example.com/m/v4\n\ngo 1.27\n", "example.com/m/v4", ""},
		{"module example.com/m/v4\r\n\r\ngo 1.27\r\n", "example.com/m/v4", ""},
		{"go 1.27\n", "", "go.mod names no module"},
	} {
		root := t.TempDir()
		put(t, root, "go.mod", tc.goMod)
		module, err := modulePath(root)
		refused := ""
		if err != nil {
			refused = err.Error()
		}
		if module != tc.module || refused != tc.err {
			t.Errorf("go.mod %q: modulePath = %q, %q; want %q, %q", tc.goMod, module, refused, tc.module, tc.err)
		}
	}
}
