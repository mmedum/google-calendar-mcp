package main

import (
	"encoding/json"
	"os"
	"slices"
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
	return []string{"google-calendar-mcp-linux-amd64", "google-calendar-mcp-linux-arm64"}
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
	// The 4.0.0 name: the family's layout is -linux-amd64.
	problems := validateManifest(good(t), bundleFiles,
		[]string{"google-calendar-mcp-linux-arm64", "google-calendar-mcp-linux-x64"})
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

// The floor is a claim about what somebody checked, so it has to name a
// version upstream actually publishes.
func TestTheCommittedManifestMeetsTheFloor(t *testing.T) {
	if behind(good(t).ManifestVersion, minManifestVersion) {
		t.Fatalf("the committed manifest is below the floor this repository claims to have checked")
	}
}

// edited is the committed manifest with one edit applied to its decoded
// document, read back the way the gate reads it.
func edited(t *testing.T, edit func(map[string]any)) manifest {
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
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
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

// Each rule the bundle's settings and claims are held to, broken once in
// the committed manifest, and the shape that ships passing all of them.
func TestTheBundleSettingsRefuseEachBreak(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{name: "the shape that ships", edit: func(map[string]any) {}},
		{"a switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
		}, "user_config.enable_destructive is a boolean defaulting to true; GCAL_ENABLE_DESTRUCTIVE is a switch " +
			"the server defaults to false"},
		{"a switch the bundle turns off", func(d map[string]any) {
			dig(d, "user_config", "sharing")["default"] = false
		}, "user_config.sharing is a boolean defaulting to false; GCAL_SHARING is a switch the server defaults to true"},
		{"a switch asked as text", func(d map[string]any) {
			dig(d, "user_config", "sharing")["type"] = "string"
			dig(d, "user_config", "sharing")["default"] = "on"
		}, `user_config.sharing is a string defaulting to "on"; GCAL_SHARING is a switch`},
		{"a switch with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "enable_decline_all"), "default")
		}, "user_config.enable_decline_all is a boolean defaulting to nothing"},
		{"an optional key with no default, as 4.0.0 shipped the client file", func(d map[string]any) {
			delete(dig(d, "user_config", "client_secret"), "default")
		}, "user_config.client_secret is optional with no default"},
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
		{"a variable the bundle does not ask for", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GCAL_LOG_LEVEL"] = "debug"
		}, "mcp_config.env sets GCAL_LOG_LEVEL, which the bundle does not ask for"},
		{"the old read-only name", func(d map[string]any) {
			env := dig(d, "server", "mcp_config", "env")
			env["GCAL_READONLY"] = env["GCAL_READ_ONLY"]
			delete(env, "GCAL_READ_ONLY")
		}, "mcp_config.env sets GCAL_READONLY, whose name is now GCAL_READ_ONLY; offer the new one"},
		{"a default that configures the server", func(d map[string]any) {
			dig(d, "user_config", "profile")["default"] = "work"
		}, "the bundle's defaults configure the server otherwise than no environment does"},
		{"a default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "profile")["default"] = "Not A Profile"
		}, "the environment the bundle's defaults give the server is refused: config: invalid: profile"},
		{"no privacy policy", func(d map[string]any) { delete(d, "privacy_policies") },
			"privacy_policies is empty"},
		{"an empty privacy policy list", func(d map[string]any) { d["privacy_policies"] = []any{} },
			"privacy_policies is empty"},
		{"no Claude Desktop floor", func(d map[string]any) { delete(dig(d, "compatibility"), "claude_desktop") },
			`compatibility.claude_desktop is ""; the family's bundles claim ">=0.10.0"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := edited(t, tc.edit)
			problems := append(claimProblems(m), bundleSettingProblems(m, serverVars())...)
			if tc.want == "" {
				if len(problems) > 0 {
					t.Fatalf("the shape that ships was refused:\n%s", strings.Join(problems, "\n"))
				}
				return
			}
			if !mentions(problems, tc.want) {
				t.Errorf("problems:\n%s\nwant one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// Every variable the server reads has a verdict: asked for, left out
// with a reason, or an old or removed name config marks. A new one with
// none fails, and so does a verdict on one the server no longer reads.
func TestEverySettingHasABundleVerdict(t *testing.T) {
	known := serverVars()
	if len(known) < 17 {
		t.Fatalf("the server reads %d variables; this would be checking a stub", len(known))
	}
	m := good(t)
	if p := bundleSettingProblems(m, known); len(p) != 0 {
		t.Fatalf("the committed manifest fails: %v", p)
	}
	got := bundleSettingProblems(m, append(slices.Clone(known), "GCAL_NEW"))
	if !mentions(got, "the server reads GCAL_NEW, and the bundle neither asks for it nor says why not") {
		t.Errorf("a setting with no verdict passed: %v", got)
	}
	without := slices.DeleteFunc(slices.Clone(known), func(s string) bool { return s == "GCAL_WRITE_TIMEOUT" })
	got = bundleSettingProblems(m, without)
	if !mentions(got, "the bundle has a verdict on GCAL_WRITE_TIMEOUT, which the server does not read") {
		t.Errorf("a verdict on a setting the server does not read passed: %v", got)
	}
}

// The launcher's install hint names the module go.mod declares, read
// under Windows line endings too. The path moves with each major
// version, so a hint left behind sends somebody to an old one.
func TestTheLauncherInstallHintIsGoModsModule(t *testing.T) {
	const hint = `echo "no binary." "Install with: go install example.com/m/v4/cmd/google-calendar-mcp@latest" >&2`
	for _, goMod := range []string{"module example.com/m/v4\n\ngo 1.27\n", "module example.com/m/v4\r\n\r\ngo 1.27\r\n"} {
		if problems := installHintProblems(hint, goMod); len(problems) > 0 {
			t.Errorf("go.mod %q: %v", goMod, problems)
		}
	}
	for _, tc := range []struct{ name, launcher, goMod, want string }{
		{"a hint without the major version", strings.Replace(hint, "/v4", "", 1), "module example.com/m/v4\n",
			`the launcher says "go install example.com/m/cmd/google-calendar-mcp@latest"`},
		{"a hint a major version behind", strings.Replace(hint, "/v4", "/v3", 1), "module example.com/m/v4\r\n",
			`the launcher says "go install example.com/m/v3/cmd/google-calendar-mcp@latest"`},
		{"no hint at all", "exit 1", "module example.com/m/v4\n", "gives no install hint"},
		{"no module line", hint, "go 1.27\n", "go.mod names no module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := installHintProblems(tc.launcher, tc.goMod); !mentions(got, tc.want) {
				t.Errorf("problems %q, want %q", got, tc.want)
			}
		})
	}
	// And the committed launcher against the committed go.mod.
	if problems := installHintProblems(launcherSource(t), string(repoFile(t, "go.mod"))); len(problems) > 0 {
		t.Errorf("the committed launcher: %v", problems)
	}
}
