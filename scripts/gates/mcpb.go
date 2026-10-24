package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
	"github.com/mmedum/google-calendar-mcp/v5/internal/server"
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

// program reports whether the bundle runs the file: an entry point or a
// binary the launcher picks. The license and the README are packed to
// be read, and nothing may spawn them.
func (f staged) program() bool { return f.platform != "" || f.launched }

// mode is the mode a staged file is packed with. What runs is 0755,
// because the desktop spawns it with the mode the zip gives it. The
// license and the README are read, and are 0644.
func (f staged) mode() os.FileMode {
	if f.program() {
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
			Command string `json:"command"`
			// Args is raw, so that a key set to null is told apart from
			// one that is missing.
			Args              json.RawMessage   `json:"args"`
			Env               map[string]string `json:"env"`
			PlatformOverrides map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"platform_overrides"`
		} `json:"mcp_config"`
	} `json:"server"`
	UserConfig    map[string]userConfigOption `json:"user_config"`
	Compatibility struct {
		ClaudeDesktop string   `json:"claude_desktop"`
		Platforms     []string `json:"platforms"`
	} `json:"compatibility"`
}

// mcpbGate runs every check against the committed manifest, on the
// names the packer stages.
func mcpbGate() error {
	_, m, problems, err := committedManifest(".")
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %s: %d staged files, %d platforms, %d user_config keys: every name resolves\n",
		manifestPath, len(bundleFiles), len(m.Compatibility.Platforms), len(m.UserConfig))
	return nil
}

// committedManifest is where the gate and the packer both start: the
// committed manifest under root, read once and decoded once, and every
// problem manifestProblems finds in it. The packer ships raw, so the
// bytes checked are the bytes shipped.
func committedManifest(root string) (raw []byte, m manifest, problems []string, err error) {
	raw, err = os.ReadFile(filepath.Join(root, manifestPath)) //nolint:gosec // a repository path from a constant
	if err != nil {
		return nil, manifest{}, nil, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, manifest{}, []string{fmt.Sprintf("the manifest does not decode: %v", err)}, nil
	}
	return raw, m, manifestProblems(raw, m, bundleFiles, root), nil
}

// manifestProblems is every check the gate and the packer both run, so
// the release packs no manifest the gate would refuse: the placeholder
// version, the no-login sentence, the manifest's own declaration and
// the vendored schema it cites, the six referential checks against the
// staged names, what the bundle says it is, the settings it asks for,
// and the launcher, go.mod and LICENSE beside it. raw is the manifest
// and m the same decoded, files the staged layout, and root the
// repository the files beside the manifest are read from. Sorted, so
// one run reads the same as the next.
func manifestProblems(raw []byte, m manifest, files []staged, root string) []string {
	problems := validateManifest(m, files)

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
	problems = append(problems, identityProblems(m, files)...)
	problems = append(problems, settingProblems(m, bundleLeftOut)...)
	problems = append(problems, launcherProblems(files, root)...)

	// And against the schema the manifest cites, which the checks above
	// only name. They hold the DECLARATION — present, pinned to an
	// immutable ref, agreeing with manifest_version, not below the floor
	// — and a document can satisfy all of that while not satisfying the
	// schema itself.
	if err := validateDocument(mcpbSchemaFile, "the committed manifest", raw); err != nil {
		problems = append(problems, err.Error())
	}

	switch want, err := licenseOf(root); {
	case err != nil:
		problems = append(problems, err.Error())
	case want != "" && m.License != want:
		problems = append(problems, fmt.Sprintf(
			"the manifest says the license is %q and LICENSE is %s", m.License, want))
	}
	sort.Strings(problems)
	return problems
}

// userConfigRef matches every ${user_config.x} in a value, not only a
// value that is nothing but one: a composed "${user_config.dir}/sub"
// would otherwise skip the check and start the server with the variable
// unsubstituted.
var userConfigRef = regexp.MustCompile(`\$\{user_config\.([A-Za-z0-9_]+)\}`)

// dirnameRef starts every command: ${__dirname} is the one path the
// specification fixes to the bundle. It shows a relative command once
// and never says what it is resolved against, and the reference host
// resolves no path (docs/architecture.md §18 row 112).
const dirnameRef = "${__dirname}/"

// validateManifest is the referential checks the manifest alone
// answers, as one function so a test can run them against a broken
// manifest without touching the repository. The sixth, the launcher's
// names, is launcherProblems', which reads the script.
//
// It takes the staged list as an argument for the same reason: each
// check is watched failing before it is believed, and a check that could
// only be exercised by breaking the real manifest would not be.
func validateManifest(m manifest, files []staged) []string {
	var problems []string

	// programs is what can be run. LICENSE and README.md are packed to be
	// read, and nothing may spawn them.
	programs := map[string]staged{}
	for _, f := range files {
		if f.program() {
			programs[f.path] = f
		}
	}

	// 1. entry_point names a program that is actually packed.
	switch _, ok := programs[m.Server.EntryPoint]; {
	case m.Server.EntryPoint == "":
		problems = append(problems, "the manifest declares no entry_point")
	case !ok:
		problems = append(problems, fmt.Sprintf("entry_point %q is not a program the packer stages",
			m.Server.EntryPoint))
	}

	// 2. every command starts with ${__dirname}/ and names a program the
	// packer stages. runs is the program each one spawns; a command that
	// names none is reported here and left out, so check 5 does not
	// report it again.
	commands := map[string]string{"": m.Server.MCPConfig.Command}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		if over.Command != "" {
			commands[platform] = over.Command
		}
	}
	runs := map[string]staged{}
	for _, platform := range slices.Sorted(maps.Keys(commands)) {
		cmd := commands[platform]
		where := "mcp_config.command"
		if platform != "" {
			where = "platform_overrides." + platform + ".command"
		}
		rel, prefixed := strings.CutPrefix(cmd, dirnameRef)
		file, isProgram := programs[rel]
		switch {
		case !prefixed:
			problems = append(problems, fmt.Sprintf("%s is %q; a command starts with %s, the one path the "+
				"specification fixes to the bundle", where, cmd, dirnameRef))
		case !isProgram:
			problems = append(problems, fmt.Sprintf("%s is %q, which is not a program the packer stages", where, cmd))
		default:
			runs[platform] = file
		}
	}

	// 3. every ${user_config.x} an env value or an argument spends is a
	// declared key. Arguments included, though settingProblems refuses one,
	// so that this check holds on its own.
	spent := map[string][]string{"mcp_config.args": m.args()}
	for key, value := range m.Server.MCPConfig.Env {
		spent["mcp_config.env."+key] = []string{value}
	}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		spent["platform_overrides."+platform+".args"] = over.Args
		for key, value := range over.Env {
			spent["platform_overrides."+platform+".env."+key] = []string{value}
		}
	}
	for _, where := range slices.Sorted(maps.Keys(spent)) {
		for _, value := range spent[where] {
			for _, ref := range userConfigRef.FindAllStringSubmatch(value, -1) {
				if _, declared := m.UserConfig[ref[1]]; !declared {
					problems = append(problems, fmt.Sprintf(
						"%s spends ${user_config.%s}, which user_config does not declare", where, ref[1]))
				}
			}
		}
	}

	// 4. every platform_overrides entry names a platform the bundle
	// claims. An override nothing can reach is the same defect as a
	// command nobody staged.
	for platform := range m.Server.MCPConfig.PlatformOverrides {
		if !slices.Contains(m.Compatibility.Platforms, platform) {
			problems = append(problems, fmt.Sprintf(
				"platform_overrides has %q, which compatibility.platforms does not claim", platform))
		}
	}

	// 5. every claimed platform spawns the file staged FOR it, not merely
	// a staged program. Deleting an override passes every check above:
	// the platform then runs the default command, which is another
	// platform's binary, and that file really is in the bundle.
	// A claimed platform nothing is staged for is identityProblems'.
	for _, platform := range m.Compatibility.Platforms {
		i := slices.IndexFunc(files, func(f staged) bool { return f.platform == platform })
		if i < 0 {
			continue
		}
		from := ""
		if _, ok := commands[platform]; ok {
			from = platform
		}
		if file, ok := runs[from]; ok && file.path != files[i].path {
			problems = append(problems, fmt.Sprintf(
				"platform %q runs %q, and the file staged for it is %q", platform, file.path, files[i].path))
		}
	}

	return problems
}

// args is mcp_config.args as strings: none when the key is missing, null
// or not a list of strings. settingProblems reports the first two, and
// the schema the third.
func (m manifest) args() []string {
	var args []string
	if err := json.Unmarshal(m.Server.MCPConfig.Args, &args); err != nil {
		return nil
	}
	return args
}

// launcherBinary matches a binary name the launcher execs.
var launcherBinary = regexp.MustCompile(`exec "\$dir/([A-Za-z0-9._-]+)"`)

// launcherProblems holds the Linux launcher under root to the packer,
// read once. It chooses between the packer's own names, which live in a
// shell script no manifest mentions, so nothing else can see them:
// renaming a staged binary otherwise passes every check and ships a
// bundle that fails for every user of that platform. And its install
// hint, given on an architecture the bundle does not carry, names the
// module go.mod declares, read by modulePath as the tag check reads it.
func launcherProblems(files []staged, root string) []string {
	script, err := os.ReadFile(filepath.Join(root, launcherPath)) //nolint:gosec // a repository path from a constant
	if err != nil {
		return []string{"cannot read the launcher: " + err.Error()}
	}
	var problems []string
	var launchable, chosen []string
	for _, f := range files {
		if f.launched {
			launchable = append(launchable, filepath.Base(f.path))
		}
	}
	for _, m := range launcherBinary.FindAllStringSubmatch(string(script), -1) {
		chosen = append(chosen, m[1])
	}
	slices.Sort(launchable)
	slices.Sort(chosen)
	if !slices.Equal(launchable, chosen) {
		problems = append(problems, fmt.Sprintf("the launcher runs [%s]; the packer stages [%s]",
			strings.Join(chosen, " "), strings.Join(launchable, " ")))
	}
	module, err := modulePath(root)
	if err != nil {
		return append(problems, err.Error())
	}
	return append(problems, installHintProblems(string(script), module)...)
}

// readManifest decodes a manifest file, for the registry entry.
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

// licenseOf reads the SPDX identifier the LICENSE file under root
// carries, so the manifest cannot claim a different one; "" for a
// license it does not recognize.
//
// It is a referential check like the others: "MIT" in a manifest beside
// an Apache license is well formed, packs, installs, and is wrong about
// the one thing a redistributor reads it for. The first draft of this
// manifest said MIT.
func licenseOf(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "LICENSE")) //nolint:gosec // a repository path from a constant
	if err != nil {
		return "", fmt.Errorf("cannot read LICENSE: %w", err)
	}
	head := string(data)
	switch {
	case strings.Contains(head, "Apache License"):
		return "Apache-2.0", nil
	case strings.Contains(head, "MIT License"):
		return "MIT", nil
	default:
		return "", nil
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

// claudeDesktopFloor is the Claude Desktop version every bundle in the
// family claims.
const claudeDesktopFloor = ">=0.10.0"

// identityProblems holds what the manifest says it is: the server's own
// name, a binary, the family's Claude Desktop floor, a privacy policy,
// and exactly the platforms the packer stages an entry point for, so
// dropping one platform and its override together is refused. The
// manifest specification requires privacy_policies of a server that
// connects to a service processing user data, as this one does to
// Google, and neither the schema nor `mcpb validate` enforces it.
// validateManifest holds each platform to the file staged for it.
func identityProblems(m manifest, files []staged) []string {
	var problems []string
	if m.Name != server.Name {
		problems = append(problems, fmt.Sprintf("the manifest's name is %q and the server's is %q", m.Name, server.Name))
	}
	if m.Server.Type != "binary" {
		problems = append(problems, fmt.Sprintf("server.type is %q; the bundle carries a binary", m.Server.Type))
	}
	if m.Compatibility.ClaudeDesktop != claudeDesktopFloor {
		problems = append(problems, fmt.Sprintf("compatibility.claude_desktop is %q; the family's bundles claim %q",
			m.Compatibility.ClaudeDesktop, claudeDesktopFloor))
	}
	var entryPoints []string
	for _, f := range files {
		if f.platform != "" {
			entryPoints = append(entryPoints, f.platform)
		}
	}
	slices.Sort(entryPoints)
	if claimed := slices.Sorted(slices.Values(m.Compatibility.Platforms)); !slices.Equal(claimed, entryPoints) {
		problems = append(problems, fmt.Sprintf("compatibility.platforms is %v; the packer stages entry points for %v",
			claimed, entryPoints))
	}
	if len(m.PrivacyPolicies) == 0 {
		problems = append(problems, "privacy_policies is empty; the manifest specification requires one for a "+
			"server that connects to a service processing user data, as this one does to Google")
	}
	return problems
}

// Why bundleLeftOut leaves a setting out.
const (
	diagnostics = "diagnostics, set by hand while chasing a problem"
	tuning      = "tuning, whose default suits an install"
)

// bundleLeftOut is every variable the server reads that the bundle does
// not ask for, with why. A removed setting is left out without an
// entry, because config says which it is.
var bundleLeftOut = map[string]string{
	config.EnvPrefix + "LOG_LEVEL":     diagnostics,
	config.EnvPrefix + "LOG_FORMAT":    diagnostics,
	config.EnvPrefix + "MAX_EVENTS":    tuning,
	config.EnvPrefix + "MAX_CALENDARS": tuning,
	config.EnvPrefix + "CONCURRENCY":   tuning,
	config.EnvPrefix + "HTTP_TIMEOUT":  tuning,
	config.EnvPrefix + "WRITE_TIMEOUT": tuning,
	config.EnvConfigDir:                "a development override, which tests move the profile with; a second account is a profile",
	config.EnvRefreshToken:             "a credential for automation; a bundle install uses the login's keyring entry",
}

// bundleVarsFrom is the settings the bundle asks for at install: every
// variable the server reads, vars, less the removed ones and less each
// one leftOut gives a reason for. A setting added later is asked for
// until the manifest asks for it or a reason is written. It also refuses
// an exclusion the server has no use for: one naming a variable it does
// not read, or one config already says is removed.
func bundleVarsFrom(vars []config.Var, leftOut map[string]string) (asked []config.Var, problems []string) {
	byName := make(map[string]config.Var, len(vars))
	for _, v := range vars {
		byName[v.Name] = v
		if !v.Removed && leftOut[v.Name] == "" {
			asked = append(asked, v)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(leftOut)) {
		v, read := byName[name]
		switch {
		case !read:
			problems = append(problems, fmt.Sprintf("bundleLeftOut names %s, which the server does not read", name))
		case v.Removed:
			problems = append(problems, fmt.Sprintf("bundleLeftOut names %s, which a release removed, "+
				"and so left out already", name))
		}
	}
	return asked, problems
}

// bundleKey is the user_config key a setting is asked under: its name
// without the prefix, lowercased, which gives the family's bundles the
// same keys.
func bundleKey(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, config.EnvPrefix))
}

// userConfigOption is the part of a user_config entry these checks read.
// Default stays raw, so an absent default is told apart from a null one,
// and value is it decoded, once, as the manifest is.
type userConfigOption struct {
	Type        string          `json:"type"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Default     json.RawMessage `json:"default"`
	value       any
}

// UnmarshalJSON decodes an entry and its default.
func (o *userConfigOption) UnmarshalJSON(data []byte) error {
	type entry userConfigOption
	if err := json.Unmarshal(data, (*entry)(o)); err != nil {
		return err
	}
	if o.Default == nil {
		return nil
	}
	return json.Unmarshal(o.Default, &o.value)
}

// fieldRule is the exact words a setting's field must carry, since the
// server refuses anything else at start, and what a field without them
// fails to do.
type fieldRule struct{ words, fails string }

// fieldRules are the settings whose field says what the server accepts.
// This server has no list of modes: sharing is a switch.
var fieldRules = map[string]fieldRule{
	config.EnvPrefix + "PROFILE": {words: config.ProfileRule, fails: "say a profile name is"},
}

// settingProblems holds what the bundle asks for to the server's own
// settings, and reports everything wrong rather than the first thing.
//
// Every setting bundleVarsFrom derives is asked for and reaches its
// variable, and nothing else is. A switch is asked as a boolean, so
// Claude Desktop shows it as one. An optional key has a default,
// because the reference host passes ${user_config.x} to the server as
// written when it has none and the person leaves the field blank (§18
// row 110). The environment the defaults give the server configures it
// as no environment does: a bundle install may turn on nothing an
// unconfigured server leaves off, because in Claude Desktop the only
// other guard is its approval of each tool, which a person can set to
// always allow (§18 row 109). The sets of switches the server refuses
// together are the ones bundleRefused declares. mcp_config.args is
// there and empty, and a platform override carries neither env nor
// args, an empty one included: the reference host replaces the base's
// with an override's rather than merging, and an argument such as
// -enable-destructive=true would configure the server past every check
// on env (§18 row 113). The profile field says what the server accepts,
// and a bundle that stops asking for it is refused, so that check cannot
// go quiet. leftOut is bundleLeftOut, or a test's.
func settingProblems(m manifest, leftOut map[string]string) []string {
	vars := config.Vars()
	asked, problems := bundleVarsFrom(vars, leftOut)

	const keepArgs = "keep it as [], since no Claude Desktop launch has shown the key optional"
	switch raw, args := m.Server.MCPConfig.Args, m.args(); {
	case raw == nil:
		problems = append(problems, "mcp_config.args is missing; "+keepArgs)
	case string(raw) == "null":
		problems = append(problems, "mcp_config.args is null; "+keepArgs)
	case len(args) > 0:
		problems = append(problems, fmt.Sprintf("mcp_config.args is %q; the bundle passes every setting "+
			"through mcp_config.env, and an argument would configure the server past these checks", args))
	}
	for _, platform := range slices.Sorted(maps.Keys(m.Server.MCPConfig.PlatformOverrides)) {
		over := m.Server.MCPConfig.PlatformOverrides[platform]
		if over.Env != nil {
			problems = append(problems, fmt.Sprintf("platform_overrides.%s.env is set; a host replaces "+
				"mcp_config.env with it, an empty one too, so put every variable in mcp_config.env", platform))
		}
		if over.Args != nil {
			problems = append(problems, fmt.Sprintf("platform_overrides.%s.args is set; a host replaces "+
				"mcp_config.args with it, an empty one too, and the bundle passes every setting through "+
				"mcp_config.env", platform))
		}
	}

	for _, key := range slices.Sorted(maps.Keys(m.UserConfig)) {
		if opt := m.UserConfig[key]; !opt.Required && opt.Default == nil {
			problems = append(problems, fmt.Sprintf("user_config.%s is optional with no default; the reference host "+
				"passes ${user_config.%s} to the server as written when the field is left blank", key, key))
		}
	}

	var switches []string
	askedName, askedKey := map[string]bool{}, map[string]bool{}
	for _, v := range asked {
		key := bundleKey(v.Name)
		askedName[v.Name], askedKey[key] = true, true
		opt, ok := m.UserConfig[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("user_config does not ask for %s, which sets %s", key, v.Name))
			continue
		}
		if got, want := m.Server.MCPConfig.Env[v.Name], "${user_config."+key+"}"; got != want {
			problems = append(problems, fmt.Sprintf("mcp_config.env.%s is %q, want %q", v.Name, got, want))
		}
		if v.Switch {
			switches = append(switches, key)
			if opt.Type != "boolean" {
				problems = append(problems, fmt.Sprintf("user_config.%s is a %s; %s is a switch, so ask for a boolean",
					key, cmp.Or(opt.Type, "field with no type"), v.Name))
			}
		}
		if rule, ok := fieldRules[v.Name]; ok && !strings.Contains(opt.Description, rule.words) {
			problems = append(problems, fmt.Sprintf("user_config.%s does not %s %q", key, rule.fails, rule.words))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(fieldRules)) {
		if !askedName[name] {
			problems = append(problems, fmt.Sprintf("the bundle asks for no %s, so nothing holds its field to %q",
				name, fieldRules[name].words))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(m.UserConfig)) {
		if !askedKey[key] {
			problems = append(problems, fmt.Sprintf("user_config asks for %s, which is no setting's key", key))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Server.MCPConfig.Env)) {
		switch {
		case askedName[name]:
		case slices.ContainsFunc(vars, func(v config.Var) bool { return v.Name == name && v.Removed }):
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which a release removed", name))
		case leftOut[name] != "":
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the bundle leaves out: %s",
				name, leftOut[name]))
		default:
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the server does not read", name))
		}
	}
	slices.Sort(switches)
	defaults, defaultsErr := defaultsProblems(m)
	problems = append(problems, defaults...)
	return append(problems, refusalProblems(m, switches, bundleRefused, defaultsErr)...)
}

// hostEnv is the environment the reference host builds from the
// bundle's defaults, with each key in on set to true whatever its
// default, as the person turning it on would. A boolean becomes "true"
// or "false", a "" default stays "", a number is written out, and a
// reference with no default is passed through as written.
//
// A variable spending a required key with no default is left out, since
// the person supplies it and it is theirs either way. One with a default
// is not: Claude Desktop's form may fill it in from the default, and a
// person may accept it unread. A default that is a list or an object is
// not guessed at: its variable is left out, and its key is in
// unreadable with what it is.
//
// It reads mcp_config.env alone. That is sound only because
// settingProblems refuses an argument and an override's env; relaxing
// either needs this to read the same.
func hostEnv(m manifest, on ...string) (env, unreadable map[string]string) {
	env, unreadable = map[string]string{}, map[string]string{}
	for name, value := range m.Server.MCPConfig.Env {
		var b strings.Builder
		skip, last := false, 0
		for _, at := range userConfigRef.FindAllStringSubmatchIndex(value, -1) {
			key, sub := value[at[2]:at[3]], value[at[0]:at[1]]
			opt := m.UserConfig[key]
			def := opt.value
			if slices.Contains(on, key) {
				def = true
			}
			switch d := def.(type) {
			case bool:
				sub = strconv.FormatBool(d)
			case string:
				sub = d
			case float64:
				sub = strconv.FormatFloat(d, 'f', -1, 64)
			case nil:
				skip = skip || opt.Required
			case []any:
				unreadable[key], skip = "a list", true
			case map[string]any:
				unreadable[key], skip = "an object", true
			}
			b.WriteString(value[last:at[0]] + sub)
			last = at[1]
		}
		if !skip {
			env[name] = b.String() + value[last:]
		}
	}
	return env, unreadable
}

// loadEnv starts the server's configuration from an environment alone.
func loadEnv(env map[string]string) (config.Config, error) {
	return config.Load(nil, func(name string) string { return env[name] })
}

// defaultsProblems reports a bundle whose defaults configure the server
// otherwise than no environment does, naming the fields that differ. A
// default that is a list or an object is reported rather than guessed
// at, every one in key order, and the comparison still runs over the
// rest. defaultsErr is what the server refuses the defaults with, which
// decides whether refusalProblems starts the sets of switches.
func defaultsProblems(m manifest) (problems []string, defaultsErr error) {
	env, unreadable := hostEnv(m)
	for _, key := range slices.Sorted(maps.Keys(unreadable)) {
		problems = append(problems, fmt.Sprintf("user_config.%s defaults to %s, which the defaults check cannot "+
			"build an environment from: give it a boolean, a string or a number", key, unreadable[key]))
	}
	got, defaultsErr := loadEnv(env)
	if defaultsErr != nil {
		problems = append(problems, fmt.Sprintf("the environment the bundle's defaults give the server is refused: %v",
			defaultsErr))
		return problems, defaultsErr
	}
	want, err := loadEnv(nil)
	if err != nil {
		return append(problems, fmt.Sprintf("an unconfigured server is refused: %v", err)), nil
	}
	if diff := configDiff(got, want); len(diff) > 0 {
		problems = append(problems, fmt.Sprintf("the bundle's defaults configure the server otherwise than no "+
			"environment does, in %s; a bundle install may turn on nothing an unconfigured server leaves off",
			strings.Join(diff, ", ")))
	}
	return problems, nil
}

// configDiff names the fields two configurations disagree on.
func configDiff(a, b config.Config) []string {
	return fieldDiff(a, b)
}

// fieldDiff names the fields of two values of one struct type that
// differ. An unexported field cannot be read out through Interface, so
// it is compared with Value.Equal where its type compares; one that does
// not, and differs, is reported unnamed.
func fieldDiff(a, b any) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	var fields []string
	unnamed := false
	for i := range va.NumField() {
		f := va.Type().Field(i)
		switch {
		case f.IsExported():
			if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
				fields = append(fields, f.Name)
			}
		case strictlyComparable(f.Type):
			if !va.Field(i).Equal(vb.Field(i)) {
				fields = append(fields, f.Name)
			}
		default:
			unnamed = true
		}
	}
	if unnamed && len(fields) == 0 && !reflect.DeepEqual(a, b) {
		fields = append(fields, "an unexported field")
	}
	return fields
}

// strictlyComparable reports whether every value of t compares with
// Value.Equal. An interface type compares as a type, but Equal panics on
// a dynamic value that does not, so one anywhere inside t rules it out.
func strictlyComparable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface:
		return false
	case reflect.Struct:
		for i := range t.NumField() {
			if !strictlyComparable(t.Field(i).Type) {
				return false
			}
		}
		return true
	case reflect.Array:
		return strictlyComparable(t.Elem())
	}
	return t.Comparable()
}

// bundleRefused are the sets of the bundle's switches the server refuses
// at start, each sorted. Each field in one names the others' titles in
// its description. Build refuses none: read-only leaves the destructive,
// sharing and decline-all switches nothing to register, and sharing off
// takes the destructive switch's two shares, rather than either pair
// being refused. The gate starts every set of the switches and holds
// the refused ones to this list, both ways, so a refusal added to Build
// fails here until it is declared and the fields say so.
var bundleRefused [][]string

// refusalProblems starts every set of the bundle's switches, each on
// over the defaults, and holds the ones the server refuses to declared:
// a refusal it does not declare, which no description then warns of,
// and a declared one the server starts with, each reported for the
// smallest set that shows it. switches is the user_config key of every
// switch the bundle asks for, sorted. A declared key that is not one of
// them is refused too, and so is one with no title, which the others'
// descriptions would name, each once. When the defaults alone do not
// start, which defaultsErr says, it starts no set: the defaults check
// reports that, and every set would fail with it.
func refusalProblems(m manifest, switches []string, declared [][]string, defaultsErr error) []string {
	problems := declaredProblems(m, switches, declared)
	if defaultsErr != nil {
		return problems
	}
	return append(problems, startProblems(m, switches, declared)...)
}

// declaredProblems holds each key in a declared set to a switch with a
// title, each once, and each switch's description to the titles of the
// others in its sets, trimmed.
func declaredProblems(m manifest, switches []string, declared [][]string) []string {
	title := func(key string) string { return strings.TrimSpace(m.UserConfig[key].Title) }
	var problems []string
	inSets := map[string]bool{}
	for _, set := range declared {
		for _, key := range set {
			inSets[key] = true
		}
	}
	titled := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(inSets)) {
		switch {
		case !slices.Contains(switches, key):
			problems = append(problems, fmt.Sprintf("bundleRefused names %s, which is not a switch the bundle asks for", key))
		case title(key) == "":
			problems = append(problems, fmt.Sprintf("user_config.%s has no title, which the fields the server "+
				"refuses it with must name", key))
		default:
			titled[key] = true
		}
	}
	for _, set := range declared {
		for _, key := range set {
			if !slices.Contains(switches, key) {
				continue
			}
			description := m.UserConfig[key].Description
			for _, other := range set {
				if other != key && titled[other] && !strings.Contains(description, title(other)) {
					problems = append(problems, fmt.Sprintf("user_config.%s does not name %q, which the server "+
						"refuses it with", key, title(other)))
				}
			}
		}
	}
	return problems
}

// startProblems starts every set of the switches over the defaults and
// holds the server's refusals to declared, both ways. Every subset of a
// set comes before it in this order, so the first set to show a defect
// is the smallest, and a superset is not reported again.
func startProblems(m manifest, switches []string, declared [][]string) []string {
	var problems []string
	var undeclared []int
	startsDeclared := map[int]bool{}
	for bits := 1; bits < 1<<len(switches); bits++ {
		var on []string
		for i, key := range switches {
			if bits&(1<<i) != 0 {
				on = append(on, key)
			}
		}
		env, _ := hostEnv(m, on...)
		_, err := loadEnv(env)
		var within []int
		for i, set := range declared {
			if !slices.ContainsFunc(set, func(key string) bool { return !slices.Contains(on, key) }) {
				within = append(within, i)
			}
		}
		switch {
		case err != nil && len(within) == 0:
			if slices.ContainsFunc(undeclared, func(shown int) bool { return shown&bits == shown }) {
				continue
			}
			undeclared = append(undeclared, bits)
			problems = append(problems, fmt.Sprintf("the server refuses %s together, and bundleRefused does not "+
				"declare it, so no field's description warns of it: %v", strings.Join(on, ", "), err))
		case err == nil:
			for _, i := range within {
				if !startsDeclared[i] {
					startsDeclared[i] = true
					problems = append(problems, fmt.Sprintf("bundleRefused declares that the server refuses %s "+
						"together, and it starts with them", strings.Join(declared[i], ", ")))
				}
			}
		}
	}
	return problems
}

// installHintProblems holds the launcher's install hint, given on an
// architecture the bundle does not carry, to module, which go.mod
// declares. The path changes with every major version from v2, so a hint
// written before one names a module that `go install` no longer updates.
func installHintProblems(launcher, module string) []string {
	want := "go install " + module + "/cmd/" + server.Name + "@latest"
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

// installHint is a `go install` command in the launcher.
var installHint = regexp.MustCompile(`go install [^\s"]+`)
