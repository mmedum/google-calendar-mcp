package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	// source is a file in this repository, for the launcher.
	source string
	// platform is the one this file is the entry point for, empty for a
	// file a launcher chooses rather than the manifest.
	platform string
	// launched marks a binary the Linux launcher picks between. Those
	// names appear in a shell script no manifest mentions, so nothing
	// else can check them.
	launched bool
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
	{path: "server/google-calendar-mcp-linux-x64", glob: "dist/*linux_amd64*/google-calendar-mcp", launched: true},
	{path: "server/google-calendar-mcp-linux-arm64", glob: "dist/*linux_arm64*/google-calendar-mcp", launched: true},
}

// manifest is the part of the document these checks are about.
type manifest struct {
	Schema          string `json:"$schema"`
	ManifestVersion string `json:"manifest_version"`
	Support         string `json:"support"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	License         string `json:"license"`
	Description     string `json:"description"`
	LongDescription string `json:"long_description"`
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
		Platforms []string `json:"platforms"`
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

	if want := licenceOf(); want != "" && m.License != want {
		problems = append(problems, fmt.Sprintf(
			"the manifest says the licence is %q and LICENSE is %s", m.License, want))
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

// licenceOf reads the SPDX identifier the repository's LICENSE file
// carries, so the manifest cannot claim a different one.
//
// It is a referential check like the others: "MIT" in a manifest beside
// an Apache licence is well formed, packs, installs, and is wrong about
// the one thing a redistributor reads it for. The first draft of this
// manifest said MIT.
func licenceOf() string {
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
//
// And `support`, which the 0.3 shape carries: a bundle that fails on
// somebody's desktop should say where to report it.
func checkManifestShape(m manifest) []string {
	var problems []string

	switch {
	case m.Schema == "":
		problems = append(problems, "the manifest has no $schema, so nothing says which version of the "+
			"format it is, and manifest_version is a claim with nothing to check it against")
	case !pinnedSchema.MatchString(m.Schema):
		problems = append(problems, fmt.Sprintf(
			"$schema is %q, which is not the pinned mcpb-manifest-v<version>.schema.json form. An "+
				"unpinned schema validates against whatever upstream serves today, which is the same "+
				"defect `pins` refuses for an action", m.Schema))
	default:
		declared := pinnedSchema.FindStringSubmatch(m.Schema)[1]
		if declared != m.ManifestVersion {
			problems = append(problems, fmt.Sprintf(
				"manifest_version is %q and $schema pins v%s; a document cannot claim one version and "+
					"validate against another", m.ManifestVersion, declared))
		}
	}

	if m.Support == "" {
		problems = append(problems, "the manifest has no support URL, so a bundle that fails on "+
			"somebody's desktop does not say where to report it")
	}
	return problems
}
