package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v4/internal/config"
	"github.com/mmedum/google-calendar-mcp/v4/internal/credentials"
	"github.com/mmedum/google-calendar-mcp/v4/internal/userconfig"
)

// The bundle gate and the packer (§12, and the standard's §10b).
//
// They are two subcommands because the useful half needs no build. The
// checks below are REFERENTIAL — they ask whether the manifest's names
// are the names something stages — and the staged names are static, so
// they can run on every commit against the committed manifest. Only the
// packing waits for a release, where the binaries exist.
//
// A schema would not catch any of this. Every failure here produces a
// manifest that is well formed, packs, installs, and then does nothing:
// an entry point nobody staged, an override for a platform the bundle
// does not claim, a platform whose command is another platform's binary,
// a ${user_config.x} that was never declared, a launcher choosing
// between names the packer does not write. Those are facts about the
// archive rather than about the document.

// manifestPath is the committed manifest, which carries a placeholder
// version so a manifest in the tree cannot claim a stale one.
const manifestPath = "packaging/mcpb/manifest.json"

// placeholderVersion is what the committed manifest must say. The packer
// refuses any other value and writes the real version through a JSON
// decode and encode, never a substitution over text.
const placeholderVersion = "0.0.0-dev"

// launcherPath is the Linux launcher, which picks a binary by
// architecture and execs it.
const launcherPath = "packaging/mcpb/launch-linux.sh"

// staged is one file the packer puts in the bundle.
//
// This list is the single source of the staged NAMES. The gate reads it
// without building anything, and the packer reads the same rows to find
// the files — so a manifest naming a file nobody stages is a fact about
// two files in this repository, found on the commit that wrote it.
type staged struct {
	// path is where the file lands inside the bundle.
	path string
	// glob finds the file under the release's dist directory. Empty for
	// a file this repository ships itself.
	glob string
	// source is a file in this repository: the launcher, the license
	// and the README.
	source string
	// platform is the one this file is the entry point for, empty for a
	// file a launcher chooses rather than the manifest.
	platform string
	// launched marks a binary the Linux launcher picks between. Those
	// names appear in a shell script no manifest mentions, so nothing
	// else can check them.
	launched bool
}

// mode is the mode a staged file is packed with. What runs is 0755,
// because the desktop spawns it with the mode the zip gives it. The
// license and the README are read, and are 0644.
func (f staged) mode() os.FileMode {
	if f.platform != "" || f.launched {
		return 0o755
	}
	return 0o644
}

// bundleFiles is what a bundle contains. The macOS slot is goreleaser's
// universal binary: a manifest has no key for the architecture, so every
// platform it claims has to work on both.
//
// The universal glob reads "universal then darwin", which looks backwards
// and is not: goreleaser names that directory <id>_darwin_all, so the id
// comes FIRST. Written the obvious way round it matched nothing, and the
// packer would have failed on the one file the macOS half of the bundle
// is. `gates release` holds every glob here against the build matrix in
// .goreleaser.yaml so neither can drift from the other again.
var bundleFiles = []staged{
	{path: "server/google-calendar-mcp-darwin", glob: "dist/*universal*darwin*/google-calendar-mcp", platform: "darwin"},
	{path: "server/google-calendar-mcp.exe", glob: "dist/*windows_amd64*/google-calendar-mcp.exe", platform: "win32"},
	{path: "server/launch-linux.sh", source: launcherPath, platform: "linux"},
	{path: "server/google-calendar-mcp-linux-amd64", glob: "dist/*linux_amd64*/google-calendar-mcp", launched: true},
	{path: "server/google-calendar-mcp-linux-arm64", glob: "dist/*linux_arm64*/google-calendar-mcp", launched: true},
	// The license, because the bundle redistributes the binaries as the
	// archives do, and the README, as in the archives.
	{path: "LICENSE", source: "LICENSE"},
	{path: "README.md", source: "README.md"},
}

// manifest is the part of the document these checks are about.
type manifest struct {
	Schema          string   `json:"$schema"`
	ManifestVersion string   `json:"manifest_version"`
	Support         string   `json:"support"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	License         string   `json:"license"`
	Description     string   `json:"description"`
	LongDescription string   `json:"long_description"`
	PrivacyPolicies []string `json:"privacy_policies"`
	Server          struct {
		Type       string `json:"type"`
		EntryPoint string `json:"entry_point"`
		MCPConfig  struct {
			Command           string            `json:"command"`
			Args              []string          `json:"args"`
			Env               map[string]string `json:"env"`
			PlatformOverrides map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"platform_overrides"`
		} `json:"mcp_config"`
	} `json:"server"`
	UserConfig    map[string]json.RawMessage `json:"user_config"`
	Compatibility struct {
		ClaudeDesktop string   `json:"claude_desktop"`
		Platforms     []string `json:"platforms"`
	} `json:"compatibility"`
}

// mcpbGate runs the referential checks against the committed manifest.
func mcpbGate() error {
	m, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	problems := validateManifest(m, bundleFiles, launcherNames())

	// The version placeholder, so a committed manifest cannot claim a
	// version that shipped.
	if m.Version != placeholderVersion {
		problems = append(problems, fmt.Sprintf(
			"the committed manifest says version %q; it must say %q and the packer writes the real one",
			m.Version, placeholderVersion))
	}
	// The first-run failure, said in the manifest because it is the one
	// a user meets: the bundle cannot log anybody in.
	if !strings.Contains(strings.ToLower(m.LongDescription), "does not log you in") {
		problems = append(problems, "long_description does not say the bundle cannot log the user in, "+
			"which is the first thing that fails for somebody who installs it")
	}
	problems = append(problems, checkManifestShape(m)...)
	problems = append(problems, bundleProblems(m)...)

	// And against the schema the manifest cites, which the checks above
	// only name. They hold the DECLARATION — present, pinned to an
	// immutable ref, agreeing with manifest_version, not below the floor
	// — and a document can satisfy all of that while not satisfying the
	// schema itself.
	raw, err := os.ReadFile(manifestPath) //nolint:gosec // a repository path from a constant
	if err != nil {
		return err
	}
	if err := validateDocument(mcpbSchemaFile, "the committed manifest", raw); err != nil {
		problems = append(problems, err.Error())
	}

	if want := licenseOf(); want != "" && m.License != want {
		problems = append(problems, fmt.Sprintf(
			"the manifest says the license is %q and LICENSE is %s", m.License, want))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}

	fmt.Printf("  %s: %d staged files, %d platforms, %d user_config keys: every name resolves\n",
		manifestPath, len(bundleFiles), len(m.Compatibility.Platforms), len(m.UserConfig))
	return nil
}

// userConfigRef matches every ${user_config.x} in a value, not only a
// value that is nothing but one: a composed "${user_config.dir}/sub"
// would otherwise skip the check and start the server with the variable
// unsubstituted.
var userConfigRef = regexp.MustCompile(`\$\{user_config\.([A-Za-z0-9_]+)\}`)

// validateManifest is the six checks, as one function so a test can run
// them against a broken manifest without touching the repository.
//
// It takes the staged list and the launcher's names as arguments for the
// same reason: each check is watched failing before it is believed, and
// a check that could only be exercised by breaking the real manifest
// would not be.
func validateManifest(m manifest, files []staged, launcher []string) []string {
	var problems []string

	stagedPaths := map[string]staged{}
	for _, f := range files {
		stagedPaths[f.path] = f
	}
	resolve := func(cmd string) string {
		return strings.TrimPrefix(strings.TrimPrefix(cmd, "${__dirname}"), "/")
	}

	// 1. entry_point names a file that is actually packed.
	if _, ok := stagedPaths[m.Server.EntryPoint]; !ok {
		problems = append(problems, fmt.Sprintf("entry_point %q is not a file the packer stages",
			m.Server.EntryPoint))
	}

	// 2. every command names one too, after stripping ${__dirname}.
	commands := map[string]string{"": m.Server.MCPConfig.Command}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		if over.Command != "" {
			commands[platform] = over.Command
		}
	}
	for platform, cmd := range commands {
		where := "mcp_config.command"
		if platform != "" {
			where = "platform_overrides." + platform + ".command"
		}
		if _, ok := stagedPaths[resolve(cmd)]; !ok {
			problems = append(problems, fmt.Sprintf("%s is %q, which the packer does not stage", where, cmd))
		}
	}

	// 3. every ${user_config.x} an env value spends is a declared key.
	envs := map[string]map[string]string{"mcp_config": m.Server.MCPConfig.Env}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		envs["platform_overrides."+platform] = over.Env
	}
	for where, env := range envs {
		for key, value := range env {
			for _, ref := range userConfigRef.FindAllStringSubmatch(value, -1) {
				if _, declared := m.UserConfig[ref[1]]; !declared {
					problems = append(problems, fmt.Sprintf(
						"%s.env.%s spends ${user_config.%s}, which user_config does not declare",
						where, key, ref[1]))
				}
			}
		}
	}

	// 4. every platform_overrides entry names a platform the bundle
	// claims. An override nothing can reach is the same defect as a
	// command nobody staged.
	claimed := map[string]bool{}
	for _, p := range m.Compatibility.Platforms {
		claimed[p] = true
	}
	for platform := range m.Server.MCPConfig.PlatformOverrides {
		if !claimed[platform] {
			problems = append(problems, fmt.Sprintf(
				"platform_overrides has %q, which compatibility.platforms does not claim", platform))
		}
	}

	// 5. every claimed platform spawns the file staged FOR it. Deleting
	// an override passes every check above: the platform then runs the
	// default command, which is another platform's binary, and that file
	// really is in the bundle.
	for _, platform := range m.Compatibility.Platforms {
		cmd, ok := commands[platform]
		if !ok {
			cmd = commands[""]
		}
		file, found := stagedPaths[resolve(cmd)]
		switch {
		case !found:
			// Already reported by check 2.
		case file.platform != platform:
			problems = append(problems, fmt.Sprintf(
				"platform %q runs %q, which is staged for %q", platform, file.path, file.platform))
		}
	}

	// 6. the launcher chooses between the packer's own names. They live
	// in a shell script no manifest mentions, so nothing above can see
	// them: renaming a staged binary otherwise passes every check and
	// ships a bundle that fails for every user of that platform.
	var launchable []string
	for _, f := range files {
		if f.launched {
			launchable = append(launchable, filepath.Base(f.path))
		}
	}
	sort.Strings(launchable)
	chosen := append([]string{}, launcher...)
	sort.Strings(chosen)
	if strings.Join(launchable, " ") != strings.Join(chosen, " ") {
		problems = append(problems, fmt.Sprintf(
			"the launcher runs [%s]; the packer stages [%s]",
			strings.Join(chosen, " "), strings.Join(launchable, " ")))
	}
	return problems
}

// launcherBinary matches a binary name the launcher execs.
var launcherBinary = regexp.MustCompile(`exec "\$dir/([A-Za-z0-9._-]+)"`)

// launcherNames reads the binaries the Linux launcher chooses between.
func launcherNames() []string {
	data, err := os.ReadFile(launcherPath)
	if err != nil {
		return nil
	}
	return launcherNamesIn(string(data))
}

// launcherNamesIn is the extraction itself, so the test that proves the
// launcher and the packer agree runs the gate's own code rather than a
// second copy of the regexp loop.
func launcherNamesIn(body string) []string {
	var out []string
	for _, m := range launcherBinary.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func readManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a repository path from a constant
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return m, nil
}

// licenseOf reads the SPDX identifier the repository's LICENSE file
// carries, so the manifest cannot claim a different one.
//
// It is a referential check like the others: "MIT" in a manifest beside
// an Apache license is well formed, packs, installs, and is wrong about
// the one thing a redistributor reads it for. The first draft of this
// manifest said MIT.
func licenseOf() string {
	data, err := os.ReadFile("LICENSE")
	if err != nil {
		return ""
	}
	head := string(data)
	switch {
	case strings.Contains(head, "Apache License"):
		return "Apache-2.0"
	case strings.Contains(head, "MIT License"):
		return "MIT"
	default:
		return ""
	}
}

// pinnedSchema matches the versioned manifest schema URL and captures the
// version it declares.
var pinnedSchema = regexp.MustCompile(`/mcpb-manifest-v(\d+\.\d+)\.schema\.json$`)

// upstreamSchema matches the host and path the schema is published at,
// and captures the ref it is served from.
//
// An allow-list on the whole URL rather than a list of refs to refuse,
// for §9.1's reason one level down: a blacklist of `main`, `master` and
// `HEAD` passes a branch called anything else, a partial tag like `v2.1`
// that upstream can re-point, and a schema served from somewhere that is
// not upstream at all.
var upstreamSchema = regexp.MustCompile(
	`^https://raw\.githubusercontent\.com/anthropics/mcpb/([^/]+)/schemas/mcpb-manifest-v\d+\.\d+\.schema\.json$`)

// immutableRef is a ref that cannot be moved under the document: a full
// release tag or a commit SHA. A partial tag is a moving ref with a
// version number in it.
var immutableRef = regexp.MustCompile(`^(v[0-9]+\.[0-9]+\.[0-9]+|[0-9a-f]{40})$`)

// minManifestVersion is the format version this repository has checked,
// and the floor a manifest may not fall below.
//
// A floor rather than an exact match, because a manifest is allowed to
// be ahead of this and not behind it. Without one the shape check is
// satisfied by any version agreeing with its own $schema — including the
// self-consistent 0.2 pair this check was written to stop spreading.
//
// Checked 2026-09-17 against the upstream schemas: 0.2, 0.3 and 0.4 are
// served and 0.5 is not. 0.4 adds one thing, a `uv` server type, and
// this bundle's server type is `binary` — so the version is deliberately
// not raised to it. Raise this floor only after checking what the newer
// format changes AND that a desktop installs a bundle declaring it;
// §18 row 77.
const minManifestVersion = "0.3"

// mcpbSchemaFile is the vendored copy of the schema a 0.3 manifest
// cites. It moves with minManifestVersion: the floor says which format
// this repository has checked, and this is the document that says what
// that format is.
const mcpbSchemaFile = "mcpb-manifest-v0.3.schema.json"

// checkManifestShape holds the manifest's own version declaration.
//
// This is the check whose absence let three of seven servers drift. The
// gate parsed manifest_version and compared it to nothing, and read
// neither $schema nor support — so a manifest could declare conformance
// to 0.2 while pointing at the UNPINNED schema path, which serves
// whatever the upstream dist/ directory currently holds. Two of those
// three were manifests written from a third as a template, which is how
// a stale shape spreads rather than being noticed.
//
// Three claims:
//
//  1. $schema is present. Without it nothing says which document this
//     is, and the contradiction below cannot even be expressed.
//  2. $schema is the PINNED, versioned file. The unpinned path is the
//     same defect this repository's pins gate exists to refuse one level
//     down: "latest" is not a version, and a green check against it
//     today says nothing about tomorrow.
//  3. The version in that URL equals manifest_version. A document
//     claiming 0.2 and validating against 0.3 is making a claim nobody
//     can check, which is worse than making none.
//  4. The URL is upstream's, at a ref that cannot move. The path pins
//     the FORMAT and the ref pins the BYTES: a branch — or a partial
//     tag, which is a branch with a version number in it — can be
//     amended under a document that claims to conform to it.
//  5. manifest_version is not below minManifestVersion. Claims 1 to 3
//     hold a manifest against ITSELF, and a stale one is perfectly
//     self-consistent — which is exactly the shape that spread.
//
// And `support`, which the 0.3 shape carries: a bundle that fails on
// somebody's desktop should say where to report it.
func checkManifestShape(m manifest) []string {
	return manifestShapeProblems(m.Schema, m.ManifestVersion, m.Support)
}

// manifestShapeProblems is the check itself, over the three values it
// reads. Taking strings rather than a struct so the sibling servers can
// call it from whatever they decode a manifest into — several read it as
// a map, because the packer rewrites one field and must not drop the
// rest.
func manifestShapeProblems(schema, manifestVersion, support string) []string {
	var problems []string

	switch {
	case schema == "":
		problems = append(problems, "the manifest has no $schema, so nothing says which version of the "+
			"format it is, and manifest_version is a claim with nothing to check it against")
	case !pinnedSchema.MatchString(schema):
		problems = append(problems, fmt.Sprintf(
			"$schema is %q, which is not the pinned mcpb-manifest-v<version>.schema.json form. An "+
				"unpinned schema validates against whatever upstream serves today, which is the same "+
				"defect `pins` refuses for an action", schema))
	case !upstreamSchema.MatchString(schema):
		problems = append(problems, fmt.Sprintf(
			"$schema is %q, which is not upstream's published path. A schema fetched from anywhere else "+
				"is not the document this format is defined by", schema))
	case !immutableRef.MatchString(upstreamSchema.FindStringSubmatch(schema)[1]):
		problems = append(problems, fmt.Sprintf(
			"$schema is served from %q, which can be re-pointed — a branch, or a partial tag. The path "+
				"pins the format and the ref pins the bytes, so an amendment upstream changes what this "+
				"document validates against. Name a full tag or a commit SHA",
			upstreamSchema.FindStringSubmatch(schema)[1]))
	default:
		declared := pinnedSchema.FindStringSubmatch(schema)[1]
		if declared != manifestVersion {
			problems = append(problems, fmt.Sprintf(
				"manifest_version is %q and $schema pins v%s; a document cannot claim one version and "+
					"validate against another", manifestVersion, declared))
		}
	}

	if behind(manifestVersion, minManifestVersion) {
		problems = append(problems, fmt.Sprintf(
			"manifest_version is %q and this repository has checked %s; a manifest agreeing with its own "+
				"$schema is still a manifest a version behind", manifestVersion, minManifestVersion))
	}

	if support == "" {
		problems = append(problems, "the manifest has no support URL, so a bundle that fails on "+
			"somebody's desktop does not say where to report it")
	}
	return problems
}

// behind reports whether a major.minor version is lower than the floor.
//
// A string comparison would read "0.10" as older than "0.3", which is
// the kind of thing that stays right until the tenth minor version.
func behind(version, floor string) bool {
	major, minor, ok := majorMinor(version)
	wantMajor, wantMinor, floorOK := majorMinor(floor)
	if !ok || !floorOK {
		return !ok // an unreadable floor is the gate's own bug, not the manifest's
	}
	if major != wantMajor {
		return major < wantMajor
	}
	return minor < wantMinor
}

func majorMinor(v string) (int, int, bool) {
	before, after, found := strings.Cut(v, ".")
	if !found {
		return 0, 0, false
	}
	major, err := strconv.Atoi(before)
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(after)
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// bundleProblems is everything the bundle claims beyond its file names:
// the claims every bundle in the family makes, the settings it asks
// for, and the launcher's install hint. The gate and the packer both
// run it.
func bundleProblems(m manifest) []string {
	problems := claimProblems(m)
	problems = append(problems, bundleSettingProblems(m, serverVars())...)
	launcher, err := os.ReadFile(launcherPath)
	if err != nil {
		return append(problems, "cannot read the launcher: "+err.Error())
	}
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		return append(problems, "cannot read go.mod: "+err.Error())
	}
	return append(problems, installHintProblems(string(launcher), string(goMod))...)
}

// claudeDesktopFloor is the Claude Desktop version every bundle in the
// family claims.
const claudeDesktopFloor = ">=0.10.0"

// claimProblems holds the two claims the schema leaves optional and the
// family makes. The manifest specification requires privacy_policies of
// a server that connects to a service processing user data, as this one
// does to Google, and neither the schema nor `mcpb validate` enforces
// it.
func claimProblems(m manifest) []string {
	var problems []string
	if len(m.PrivacyPolicies) == 0 {
		problems = append(problems, "privacy_policies is empty; the manifest specification requires one for a "+
			"server that connects to a service processing user data, as this one does to Google")
	}
	if m.Compatibility.ClaudeDesktop != claudeDesktopFloor {
		problems = append(problems, fmt.Sprintf("compatibility.claude_desktop is %q; the family's bundles claim %q",
			m.Compatibility.ClaudeDesktop, claudeDesktopFloor))
	}
	return problems
}

// bundleAsked are the settings the bundle asks for at install: what a
// person sets in another client's configuration. Each is asked under
// its name without the prefix, lowercased, which gives the family's
// bundles the same keys.
var bundleAsked = []string{
	config.EnvPrefix + "CLIENT_SECRET",
	config.EnvPrefix + "PROFILE",
	config.EnvPrefix + "READ_ONLY",
	config.EnvPrefix + "ENABLE_DESTRUCTIVE",
	config.EnvPrefix + "SHARING",
	config.EnvPrefix + "ENABLE_DECLINE_ALL",
}

// Why bundleLeftOut leaves a setting out.
const (
	diagnostics = "diagnostics, set by hand while chasing a problem"
	tuning      = "tuning, whose default suits an install"
)

// bundleLeftOut is every other variable the server reads, with why the
// bundle does not ask for it. An old or removed name is left out
// without an entry, because config says which it is.
var bundleLeftOut = map[string]string{
	config.EnvPrefix + "LOG_LEVEL":     diagnostics,
	config.EnvPrefix + "LOG_FORMAT":    diagnostics,
	config.EnvPrefix + "MAX_EVENTS":    tuning,
	config.EnvPrefix + "MAX_CALENDARS": tuning,
	config.EnvPrefix + "CONCURRENCY":   tuning,
	config.EnvPrefix + "HTTP_TIMEOUT":  tuning,
	config.EnvPrefix + "WRITE_TIMEOUT": tuning,
	userconfig.EnvDir:                  "a development override, which tests move the profile with; a second account is a profile",
	credentials.EnvVar:                 "a credential for automation; a bundle install uses the login's keyring entry",
}

// serverVars is every variable the server reads: what Define looks up,
// and the two read outside it, named by their own constants so a rename
// is a compile error here rather than a shorter list.
func serverVars() []string {
	vars := append(config.EnvVars(), userconfig.EnvDir, credentials.EnvVar)
	slices.Sort(vars)
	return slices.Compact(vars)
}

// bundleKey is the user_config key a setting is asked under.
func bundleKey(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, config.EnvPrefix))
}

// userConfigOption is the part of a user_config entry these checks read.
// Default stays raw, so an absent default and a JSON comparison are both
// possible.
type userConfigOption struct {
	Type     string          `json:"type"`
	Required bool            `json:"required"`
	Default  json.RawMessage `json:"default"`
}

// bundleSettingProblems holds what the bundle asks for to the server's
// own settings. known is every variable the server reads, an argument so
// a test can add one.
//
// Every variable is asked for or left out with a reason, and each one
// asked for reaches its variable and nothing else is set. A switch
// defaults to the server's own default, compared as JSON so a text
// "false" is refused: in Claude Desktop the only guard besides the
// switch is its approval of each tool, which a person can set to always
// allow, so a bundle install may turn on nothing an unconfigured server
// leaves off, and turn off nothing it leaves on. An optional key has a
// default, because the reference host passes ${user_config.x} to the
// server as written when it has none and the person leaves the field
// blank. And the environment the defaults give the server configures it
// as no environment does.
func bundleSettingProblems(m manifest, known []string) []string {
	vars := map[string]config.Var{}
	for _, v := range config.Vars() {
		vars[v.Name] = v
	}

	var problems []string
	for _, name := range known {
		v := vars[name]
		if !slices.Contains(bundleAsked, name) && bundleLeftOut[name] == "" && v.ReplacedBy == "" && !v.Removed {
			problems = append(problems, fmt.Sprintf("the server reads %s, and the bundle neither asks for it "+
				"nor says why not", name))
		}
	}
	for _, name := range slices.Concat(bundleAsked, slices.Sorted(maps.Keys(bundleLeftOut))) {
		if !slices.Contains(known, name) {
			problems = append(problems, fmt.Sprintf("the bundle has a verdict on %s, which the server does not read", name))
		}
	}

	options := map[string]userConfigOption{}
	for _, key := range slices.Sorted(maps.Keys(m.UserConfig)) {
		var opt userConfigOption
		if err := json.Unmarshal(m.UserConfig[key], &opt); err != nil {
			problems = append(problems, fmt.Sprintf("user_config.%s is not an object: %v", key, err))
			continue
		}
		options[key] = opt
		if !opt.Required && opt.Default == nil {
			problems = append(problems, fmt.Sprintf("user_config.%s is optional with no default; the reference host "+
				"passes ${user_config.%s} to the server as written when the field is left blank", key, key))
		}
	}

	asked := map[string]bool{}
	for _, name := range bundleAsked {
		key := bundleKey(name)
		asked[key] = true
		opt, ok := options[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("user_config does not ask for %s, which sets %s", key, name))
			continue
		}
		if got, want := m.Server.MCPConfig.Env[name], "${user_config."+key+"}"; got != want {
			problems = append(problems, fmt.Sprintf("mcp_config.env.%s is %q, want %q", name, got, want))
		}
		v := vars[name]
		if !v.Switch {
			continue
		}
		var got bytes.Buffer
		if opt.Default == nil || json.Compact(&got, opt.Default) != nil || got.String() != v.Default {
			problems = append(problems, fmt.Sprintf("user_config.%s is a %s defaulting to %s; %s is a switch the "+
				"server defaults to %s, and a bundle install may change nothing an unconfigured server does",
				key, cmp.Or(opt.Type, "field"), cmp.Or(got.String(), "nothing"), name, v.Default))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(options)) {
		if !asked[key] {
			problems = append(problems, fmt.Sprintf("user_config asks for %s, which is no setting's key", key))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Server.MCPConfig.Env)) {
		switch {
		case vars[name].ReplacedBy != "":
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, whose name is now %s; offer the new one",
				name, vars[name].ReplacedBy))
		case !slices.Contains(bundleAsked, name):
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the bundle does not ask for", name))
		}
	}
	if p := defaultsProblem(m.Server.MCPConfig.Env, options); p != "" {
		problems = append(problems, p)
	}
	return problems
}

// defaultsProblem reports a bundle whose defaults configure the server
// otherwise than no environment does.
//
// The environment is built as the reference host builds it: a boolean
// becomes "true" or "false", a text default stays as it is, and a
// reference with no default is left as written. A setting the person
// must supply is left out, since it is theirs either way.
func defaultsProblem(env map[string]string, options map[string]userConfigOption) string {
	given := map[string]string{}
	for name, value := range env {
		required := false
		given[name] = userConfigRef.ReplaceAllStringFunc(value, func(ref string) string {
			opt := options[userConfigRef.FindStringSubmatch(ref)[1]]
			required = required || opt.Required
			var d any
			if json.Unmarshal(opt.Default, &d) != nil {
				return ref
			}
			switch d := d.(type) {
			case bool:
				return strconv.FormatBool(d)
			case string:
				return d
			case float64:
				return strconv.FormatFloat(d, 'f', -1, 64)
			}
			return ref
		})
		if required {
			delete(given, name)
		}
	}
	build := func(env map[string]string) (config.Config, error) {
		fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		return config.Define(fs, func(name string) string { return env[name] }).Build()
	}
	got, err := build(given)
	if err != nil {
		return fmt.Sprintf("the environment the bundle's defaults give the server is refused: %v", err)
	}
	want, err := build(nil)
	if err != nil {
		return fmt.Sprintf("an unconfigured server is refused: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("the bundle's defaults configure the server otherwise than no environment does: %v", given)
	}
	return ""
}

// installHintProblems holds the launcher's install hint, given on an
// architecture the bundle does not carry, to the module go.mod declares.
// The path changes with every major version from v2, so a hint written
// before one names a module that `go install` no longer updates.
func installHintProblems(launcher, goMod string) []string {
	module := moduleLine.FindStringSubmatch(goMod)
	if module == nil {
		return []string{"go.mod names no module"}
	}
	want := "go install " + module[1] + "/cmd/google-calendar-mcp@latest"
	hints := installHint.FindAllString(launcher, -1)
	if len(hints) == 0 {
		return []string{"the launcher gives no install hint for an architecture the bundle does not carry"}
	}
	var problems []string
	for _, hint := range hints {
		if hint != want {
			problems = append(problems, fmt.Sprintf("the launcher says %q, and go.mod's module gives %q", hint, want))
		}
	}
	return problems
}

var (
	// moduleLine is go.mod's module directive. Trailing space is allowed
	// because a checkout with Windows line endings ends it with "\r".
	moduleLine  = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
	installHint = regexp.MustCompile(`go install [^\s"]+`)
)
