package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
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

func testLauncher() []string {
	return []string{"google-calendar-mcp-linux-arm64", "google-calendar-mcp-linux-x64"}
}

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

func TestTheCommittedManifestIsValid(t *testing.T) {
	if problems := validateManifest(good(t), bundleFiles, testLauncher()); len(problems) > 0 {
		t.Fatalf("the committed manifest does not describe the bundle:\n%s", strings.Join(problems, "\n"))
	}
}

func TestTheSixWaysAManifestBreaks(t *testing.T) {
	cases := []struct {
		name   string
		breaks func(m *manifest)
		want   string
	}{
		{
			name:   "an entry point nobody stages",
			breaks: func(m *manifest) { m.Server.EntryPoint = "server/google-calendar-mcp-mac" },
			want:   "entry_point",
		},
		{
			name: "a platform command nobody stages",
			breaks: func(m *manifest) {
				over := m.Server.MCPConfig.PlatformOverrides["win32"]
				over.Command = "${__dirname}/server/google-calendar-mcp-windows.exe"
				m.Server.MCPConfig.PlatformOverrides["win32"] = over
			},
			want: "platform_overrides.win32.command",
		},
		{
			name: "an env value spending a key nobody declared",
			breaks: func(m *manifest) {
				// Composed, not the whole value: a check that only looked
				// at values that ARE a reference would miss this one and
				// the server would start with ${user_config.dir}
				// unsubstituted.
				m.Server.MCPConfig.Env["GCAL_CONFIG_DIR"] = "${user_config.dir}/google-calendar-mcp"
			},
			want: "user_config does not declare",
		},
		{
			name: "an override for a platform the bundle does not claim",
			breaks: func(m *manifest) {
				m.Server.MCPConfig.PlatformOverrides["freebsd"] = m.Server.MCPConfig.PlatformOverrides["linux"]
			},
			want: "compatibility.platforms does not claim",
		},
		{
			name: "a platform running another platform's binary",
			breaks: func(m *manifest) {
				// Deleting the override passes every check above: win32
				// then runs the default command, which is the macOS
				// universal binary, and that file really is in the bundle.
				delete(m.Server.MCPConfig.PlatformOverrides, "win32")
			},
			want: `platform "win32" runs`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := good(t)
			c.breaks(&m)
			problems := validateManifest(m, bundleFiles, testLauncher())
			if !mentions(problems, c.want) {
				t.Fatalf("breaking %s was not refused; problems: %v", c.name, problems)
			}
		})
	}
}

// TestTheLauncherNamesAreThePackers is the sixth way, and the one no
// manifest can see: the names live in a shell script nothing else reads.
func TestTheLauncherNamesAreThePackers(t *testing.T) {
	problems := validateManifest(good(t), bundleFiles, []string{"google-calendar-mcp-linux-amd64"})
	if !mentions(problems, "the launcher runs") {
		t.Fatalf("a launcher running a binary nobody stages was not refused; problems: %v", problems)
	}
	// And the real launcher agrees with the real staged list, read from
	// the file rather than from the list above.
	names := launcherNamesIn(launcherSource(t))
	if len(names) != 2 {
		t.Fatalf("the launcher execs %d binaries, want 2: %v", len(names), names)
	}
	if problems := validateManifest(good(t), bundleFiles, names); len(problems) > 0 {
		t.Fatalf("the committed launcher and the packer disagree:\n%s", strings.Join(problems, "\n"))
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

// TestTheCommittedManifestCarriesThePlaceholder: a manifest in the tree
// must not be able to claim a version that shipped.
func TestTheCommittedManifestCarriesThePlaceholder(t *testing.T) {
	if got := good(t).Version; got != placeholderVersion {
		t.Fatalf("the committed manifest says %q, want the placeholder %q", got, placeholderVersion)
	}
}

// TestTheManifestSaysItCannotLogYouIn is the first-run failure: an
// install that appears to work and then answers every call with "not
// signed in".
func TestTheManifestSaysItCannotLogYouIn(t *testing.T) {
	if !strings.Contains(strings.ToLower(good(t).LongDescription), "does not log you in") {
		t.Fatal("long_description does not say the bundle cannot log the user in")
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

// The three ways a manifest can misdeclare its own version, and the
// missing support URL. This check did not exist while three of the seven
// servers drifted apart — one carried no $schema, and two declared
// manifest_version 0.2 while pointing at the UNPINNED schema path, which
// serves whatever upstream publishes today. Two of those were written
// from the third as a template, which is how a stale shape spreads.
func TestTheWaysAManifestMisdeclaresItsVersion(t *testing.T) {
	const pinned = "https://raw.githubusercontent.com/anthropics/mcpb/main/schemas/mcpb-manifest-v0.3.schema.json"

	good := manifest{Schema: pinned, ManifestVersion: "0.3", Support: "https://example.invalid/issues"}
	if problems := checkManifestShape(good); len(problems) > 0 {
		t.Fatalf("a well-formed manifest was refused:\n%s", strings.Join(problems, "\n"))
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
