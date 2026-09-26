package main

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The release gate (§12, and the standard's §9 and §10b).
//
// It is the third member of the family `mcpb` belongs to, and it holds
// the same KIND of claim: not "is this file well formed" but "do these
// two files name the same thing". The bundle gate holds the manifest
// against the staged names; this one holds the staged names against the
// build that produces them, and the pack hook against the Makefile.
//
// It exists because phase 4 wrote `dist/*darwin*universal*/…` into the
// packer while there was no goreleaser config to check it against.
// goreleaser names that directory <id>_darwin_all, so the id comes first
// and the glob matched nothing. Every gate was green: the packer only
// runs at release time, and the release did not exist yet. That is the
// most expensive moment there is to find a typo, which is the argument
// for reading the config on every commit instead.
//
// Nothing here builds anything. The config is static, the staged names
// are static, and what a build would put under dist/ is derivable from
// the two — so this runs in `make check` beside the bundle gate, and
// only the packing waits for a tag.
//
// A YAML parse rather than the line scanners in pins.go and parity.go:
// those read one key at a fixed depth, and this reads nested lists whose
// shape is the whole question. A hand-rolled reader that silently found
// no `universal_binaries` would report a clean config, which is the
// failure this gate is for.

const (
	goreleaserPath      = ".goreleaser.yaml"
	releaseWorkflowPath = ".github/workflows/release.yml"
	makefilePath        = "Makefile"
)

// extraFile is one `extra_files` entry, in checksum and release alike.
type extraFile struct {
	Glob string `yaml:"glob"`
}

// goreleaserConfig is the part of the config these checks are about.
// Anything not read here is deliberately not this gate's business.
type goreleaserConfig struct {
	// ProjectName is what a universal binary is called when its
	// name_template does not say otherwise, which is not the same thing
	// as the build's `binary`.
	ProjectName string `yaml:"project_name"`
	Builds      []struct {
		ID     string   `yaml:"id"`
		Binary string   `yaml:"binary"`
		Goos   []string `yaml:"goos"`
		Goarch []string `yaml:"goarch"`
		// Ignore removes combinations from the matrix. Unmodeled, a
		// release that ships five archives passes every check here.
		Ignore []struct {
			Goos   string `yaml:"goos"`
			Goarch string `yaml:"goarch"`
		} `yaml:"ignore"`
		Flags        []string `yaml:"flags"`
		ModTimestamp string   `yaml:"mod_timestamp"`
		Ldflags      []string `yaml:"ldflags"`
	} `yaml:"builds"`
	UniversalBinaries []struct {
		ID           string   `yaml:"id"`
		IDs          []string `yaml:"ids"`
		Replace      bool     `yaml:"replace"`
		NameTemplate string   `yaml:"name_template"`
		Hooks        struct {
			Post string `yaml:"post"`
		} `yaml:"hooks"`
	} `yaml:"universal_binaries"`
	Archives []struct {
		IDs []string `yaml:"ids"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string      `yaml:"name_template"`
		ExtraFiles   []extraFile `yaml:"extra_files"`
	} `yaml:"checksum"`
	SBOMs []struct {
		Artifacts string `yaml:"artifacts"`
	} `yaml:"sboms"`
	Signs []struct {
		Cmd       string   `yaml:"cmd"`
		Signature string   `yaml:"signature"`
		Args      []string `yaml:"args"`
		Artifacts string   `yaml:"artifacts"`
	} `yaml:"signs"`
	// Present at all is the defect. `changelog: disable: true` reads like
	// the way to leave --release-notes in charge and is the opposite:
	// Disable is evaluated in the changelog pipe's Skip, which runs
	// before Run, so the notes file is never opened and the release body
	// collapses to the footer alone — with every step still green.
	Changelog *yaml.Node `yaml:"changelog"`
	Release   struct {
		Draft      bool        `yaml:"draft"`
		ExtraFiles []extraFile `yaml:"extra_files"`
	} `yaml:"release"`
}

// buildTarget is one directory a build puts a binary in, under dist/.
type buildTarget struct {
	goos, goarch string
	// dir is the name goreleaser documents for that directory,
	// `<id>_<goos>_<goarch>`. A real build appends a variant to it —
	// `_v1` for GOAMD64, `_v8.0` for GOARM64 — which is why every staged
	// glob ends its directory component with `*`.
	//
	// That variant is deliberately NOT modeled. Guessing it here would
	// put the guess on both sides of the comparison, so a wrong one
	// would make the gate and the packer agree and the release still
	// fail. Matching against the documented name instead turns a glob
	// that hardcodes `_v1` into a failure here, which is the rule worth
	// having: the bundle must not depend on a suffix goreleaser can
	// change.
	dir string
	// binary is the file inside it, with the extension the platform
	// needs. A glob naming the wrong one packs a bundle whose Windows
	// slot is empty.
	binary string
}

// manifestPlatform maps a bundle manifest's platform name to a GOOS.
// They are not the same vocabulary: a manifest says win32 for a 64-bit
// Windows build, and reading one as the other is how a bundle ends up
// claiming a platform it does not carry.
var manifestPlatform = map[string]string{
	"darwin": "darwin",
	"win32":  "windows",
	"linux":  "linux",
}

// releaseGate runs the referential checks against the committed config.
func releaseGate() error {
	cfg, err := readGoreleaser(goreleaserPath)
	if err != nil {
		return err
	}
	flow, err := readWorkflow(releaseWorkflowPath)
	if err != nil {
		return fmt.Errorf("%s: %w (the release this repository ships has to exist as a file "+
			"before anything can hold it to anything)", releaseWorkflowPath, err)
	}
	out, err := makeVariable(makefilePath, "MCPB_OUT")
	if err != nil {
		return err
	}

	problems, targets := validateRelease(cfg, bundleFiles, out, flow)

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}

	fmt.Printf("  %s: %d build targets, %d staged globs resolved, bundle signed and uploaded as %s\n",
		goreleaserPath, len(targets), globCount(bundleFiles), normalizeVersion(out))
	return nil
}

// validateRelease is the checks themselves, taking everything they read
// as arguments so a test can run them against a broken config without
// touching the repository. A check that could only be exercised by
// breaking the real config would not be watched failing, and a check
// nobody has watched fail is not yet a check.
func validateRelease(cfg goreleaserConfig, files []staged, mcpbOut string, flow workflow) ([]string, []buildTarget) {
	var problems []string

	targets := buildTargets(cfg)

	// The floors first, and as ordinary problems rather than an early
	// return, so the tests that drive this function can reach them.
	// "Found nothing" and "looked at nothing" print the same sentence.
	// Six PLATFORM archives. The universal binary is a seventh target and
	// not an archive, so counting it here would let a matrix of five
	// platforms clear a floor of six — and `ignore:` is modeled above
	// for the same reason.
	platforms := 0
	for _, t := range targets {
		if t.goarch != "all" {
			platforms++
		}
	}
	if platforms < 6 {
		problems = append(problems, fmt.Sprintf(
			"%s builds %d platform targets and §12 says six archives; the matrix is short, "+
				"or the builds block is not being read", goreleaserPath, platforms))
	}
	if len(files) < 5 {
		problems = append(problems, fmt.Sprintf(
			"the packer stages %d files; that is not the bundle", len(files)))
	}

	// 1. Every staged glob resolves to exactly ONE target the build
	// matrix produces, and to the binary that target actually writes.
	//
	// This is the check that would have caught the darwin glob. A glob
	// matching nothing fails the packer at release time; a glob matching
	// two packs whichever sorted first, and a bundle built from the
	// wrong binary is not something a checksum catches — the checksum is
	// of whatever was built.
	for _, f := range files {
		if f.glob == "" {
			continue
		}
		dir, base := path.Split(trimDistPrefix(f.glob))
		dir = strings.TrimSuffix(dir, "/")
		if dir == "" || base == "" {
			problems = append(problems, fmt.Sprintf(
				"%s: the glob %q is not dist/<directory>/<binary>", f.path, f.glob))
			continue
		}

		var matched []buildTarget
		for _, t := range targets {
			if t.matches(dir) {
				matched = append(matched, t)
			}
		}
		switch len(matched) {
		case 0:
			problems = append(problems, fmt.Sprintf(
				"%s: the glob %q matches no directory %s builds (it builds %s)",
				f.path, f.glob, goreleaserPath, targetNames(targets)))
			continue
		case 1:
		default:
			problems = append(problems, fmt.Sprintf(
				"%s: the glob %q matches %d of the build targets (%s); it must name exactly one, "+
					"because a bundle packed from the wrong binary is not something a checksum catches",
				f.path, f.glob, len(matched), targetNames(matched)))
			continue
		}

		t := matched[0]
		if base != t.binary {
			problems = append(problems, fmt.Sprintf(
				"%s: the glob ends in %q and %s/%s writes %q",
				f.path, base, t.goos, t.goarch, t.binary))
		}
		// 2. And the target is for the platform the manifest spawns this
		// file on. A glob that resolves cleanly to the wrong platform's
		// directory passes every check above and ships a bundle whose
		// macOS entry is a Linux binary.
		if f.platform != "" {
			if want := manifestPlatform[f.platform]; want != "" && want != t.goos {
				problems = append(problems, fmt.Sprintf(
					"%s is staged for the manifest platform %q and its glob resolves to %s/%s",
					f.path, f.platform, t.goos, t.goarch))
			}
		}
	}

	// 3. Exactly one universal binary, and its post hook packs the
	// bundle. The hook is the one point in the pipeline where every
	// binary exists and checksums.txt has not been written, which is
	// what MAKES it possible for the bundle to be in that file and
	// therefore under the signature.
	switch len(cfg.UniversalBinaries) {
	case 1:
		u := cfg.UniversalBinaries[0]
		hook := u.Hooks.Post
		switch {
		case hook == "":
			problems = append(problems, "the universal binary has no post hook, so nothing packs the "+
				"bundle at the only point where it can still reach checksums.txt")
		case !strings.Contains(hook, "mcpb-pack"):
			problems = append(problems, fmt.Sprintf(
				"the universal binary's post hook does not run mcpb-pack: %q", hook))
		case !strings.Contains(normalizeVersion(hook), normalizeVersion(mcpbOut)):
			// The Makefile and the hook are two files naming one path,
			// and you are only ever editing one of them. They necessarily
			// spell the version differently — make expands $(VERSION),
			// goreleaser expands {{ .Version }} — so both are reduced to
			// the same placeholder before they are compared, and what is
			// held is the pattern rather than one rendering of it.
			problems = append(problems, fmt.Sprintf(
				"the post hook packs to a path the Makefile does not name: MCPB_OUT is %q and the hook is %q",
				mcpbOut, hook))
		}
		if u.Replace {
			problems = append(problems, "universal_binaries.replace is true, which removes the per-architecture "+
				"macOS archives the release page needs")
		}
		// A universal binary is lipo over the darwin builds, so the ids
		// it names have to BE built for darwin. Without this the macOS
		// glob still resolves — buildTargets adds the universal
		// directory from this block rather than from the matrix — and
		// the bundle's macOS slot silently has no source.
		if !buildsDarwin(cfg, u.IDs) {
			problems = append(problems, fmt.Sprintf(
				"the universal binary is built from %v, which goreleaser does not build for darwin; "+
					"there is nothing for it to join", u.IDs))
		}
	case 0:
		problems = append(problems, "no universal_binaries block: a manifest names a command per platform and "+
			"has no key for the architecture, so the macOS entry has to work on both")
	default:
		problems = append(problems, fmt.Sprintf(
			"%d universal binaries; the bundle stages one macOS file", len(cfg.UniversalBinaries)))
	}

	// 4. The bundle is checksummed AND uploaded. Both, or it ships
	// unsigned, or is hashed and never published — and neither looks any
	// different on the release page from the correct build.
	bundle := normalizeVersion(mcpbOut)
	if !namesFile(cfg.Checksum.ExtraFiles, bundle) {
		problems = append(problems, fmt.Sprintf(
			"checksum.extra_files does not cover %s, so the bundle is not in checksums.txt and therefore "+
				"not under the signature, which is over that file", mcpbOut))
	}
	if !namesFile(cfg.Release.ExtraFiles, bundle) {
		problems = append(problems, fmt.Sprintf(
			"release.extra_files does not cover %s, so the bundle is hashed and never uploaded: "+
				"a checksum of something nobody can download", mcpbOut))
	}

	// 5. The archives exclude the universal binary by id. Without it
	// macOS gets a fourth download on the release page, which is a
	// choice nobody should have to make.
	if len(cfg.Archives) == 0 {
		problems = append(problems, "no archives block")
	}
	for i, a := range cfg.Archives {
		if len(a.IDs) == 0 {
			problems = append(problems, fmt.Sprintf(
				"archives[%d] names no ids, so it archives the universal binary too", i))
			continue
		}
		for _, u := range cfg.UniversalBinaries {
			if contains(a.IDs, u.ID) {
				problems = append(problems, fmt.Sprintf(
					"archives[%d] includes the universal id %q", i, u.ID))
			}
		}
	}

	// 6. Reproducibility, which is the claim §12 makes out loud.
	for _, b := range cfg.Builds {
		if !contains(b.Flags, "-trimpath") {
			problems = append(problems, fmt.Sprintf(
				"build %q does not pass -trimpath, so the binary carries this machine's paths", b.ID))
		}
		if !strings.Contains(b.ModTimestamp, "CommitTimestamp") {
			problems = append(problems, fmt.Sprintf(
				"build %q does not stamp mod_timestamp from the commit, so rebuilding a tag "+
					"does not reproduce it", b.ID))
		}
		// The binary's own --version is one of the five places the
		// version has to agree. Without this it reports the module
		// version or "dev" forever, and says so confidently.
		if !strings.Contains(strings.Join(b.Ldflags, " "), "version.Version=") {
			problems = append(problems, fmt.Sprintf(
				"build %q does not stamp internal/version.Version, so the binary cannot report "+
					"which release it is", b.ID))
		}
	}

	// 7. An SBOM per archive, and a keyless signature over the checksum
	// file.
	if !anySBOMOver(cfg, "archive") {
		problems = append(problems, "no sboms block over the archives")
	}
	problems = append(problems, checkSigns(cfg)...)

	// 8. A tag publishes a real release. A draft nobody remembers to
	// publish is how releases go missing.
	if cfg.Release.Draft {
		problems = append(problems, "release.draft is true, so a tag publishes nothing until somebody "+
			"remembers to press a button")
	}

	// 9. No changelog block, and the workflow passes the notes.
	if cfg.Changelog != nil {
		problems = append(problems, "there is a changelog block: `disable` is read in the pipe's Skip, "+
			"before the notes file is opened, so the release body collapses to the footer alone. "+
			"Deleting the block is what leaves --release-notes in charge")
	}
	problems = append(problems, checkWorkflow(cfg, flow)...)

	return problems, targets
}

// checkWorkflow holds the release workflow against the config.
//
// Read as structure rather than as text. The first version asked
// `strings.Contains(workflow, "--release-notes")`, which cannot tell the
// flag being PASSED to goreleaser from the same word in a comment, and
// needed a second clause for shell quoting that turned out to be
// unreachable. A step's `args` is one field.
func checkWorkflow(cfg goreleaserConfig, flow workflow) []string {
	var problems []string

	// A tag is what publishes. A workflow only a person can start is one
	// nobody remembers to start.
	if !flow.triggersOnTag("v") {
		problems = append(problems, releaseWorkflowPath+" does not trigger on a v* tag")
	}

	writesNotes, passesNotes, attests := false, false, false
	checksTag, built := false, false
	for _, step := range flow.steps() {
		if strings.Contains(step.Run, "release-notes") {
			writesNotes = true
		}
		// Before goreleaser, or the refusal comes after the publish.
		if strings.Contains(step.Run, "release-tag") && !built {
			checksTag = true
		}
		if strings.HasPrefix(step.Uses, "goreleaser/goreleaser-action") {
			built = true
			if args, ok := step.input("args"); ok && strings.Contains(args, "--release-notes") {
				passesNotes = true
			}
		}
		if !strings.HasPrefix(step.Uses, "actions/attest-build-provenance") {
			continue
		}
		attests = true
		subjects, _ := step.input("subject-path")
		// The checksum file is the one everything else is verified
		// through, and its name lives in the config. Two files naming
		// one artifact, so they are held against each other.
		if want := cfg.Checksum.NameTemplate; want != "" && !strings.Contains(subjects, want) {
			problems = append(problems, fmt.Sprintf(
				"the provenance covers %q and the checksum file is called %q, so the file every "+
					"other artifact is verified through carries no attestation", subjects, want))
		}
		if !strings.Contains(subjects, ".mcpb") {
			problems = append(problems, "the provenance does not cover the bundle, which is the file "+
				"most people install")
		}
	}
	if !passesNotes {
		problems = append(problems, releaseWorkflowPath+" does not pass --release-notes to goreleaser, "+
			"so the release body is generated from commit subjects")
	}
	if !writesNotes {
		problems = append(problems, releaseWorkflowPath+" never runs `gates release-notes`, so nothing "+
			"writes the notes file it passes")
	}
	if !checksTag {
		problems = append(problems, releaseWorkflowPath+" does not run `gates release-tag` before goreleaser, "+
			"so a tag whose major version is not go.mod's publishes a release `go install ...@latest` never serves")
	}
	if !attests {
		problems = append(problems, releaseWorkflowPath+" runs no build-provenance attestation")
	}
	return problems
}

// checkSigns holds the signing block, which is the one step that cannot
// be rehearsed: keyless signing needs an OIDC token, so `goreleaser
// check` and a full local snapshot both pass while it is wrong.
func checkSigns(cfg goreleaserConfig) []string {
	var problems []string
	signed := false
	for _, s := range cfg.Signs {
		if s.Artifacts != "checksum" {
			continue
		}
		signed = true
		if s.Cmd != "cosign" {
			problems = append(problems, fmt.Sprintf(
				"the checksum file is signed with %q rather than cosign, so the verification the release "+
					"page documents does not apply to it", s.Cmd))
		}
		// --bundle writes the signature and the certificate together, so
		// the output name says which of the two conventions this is.
		if !strings.HasSuffix(s.Signature, ".bundle") {
			problems = append(problems, fmt.Sprintf(
				"the signature is written to %q; cosign 3 writes one bundle carrying both the signature "+
					"and the certificate, and the release page tells people to pass --bundle", s.Signature))
		}
		args := strings.Join(s.Args, " ")
		if !strings.Contains(args, "--bundle") {
			problems = append(problems, "the signature does not pass --bundle, which cosign 3 made "+
				"REQUIRED: with only --output-signature and --output-certificate it is given no output "+
				"path at all and fails with \"create bundle file: open : no such file or directory\"")
		}
		if !strings.Contains(args, "--yes") {
			problems = append(problems, "the signature does not pass --yes, so cosign waits for a "+
				"confirmation nobody is there to give")
		}
	}
	if !signed {
		problems = append(problems, "nothing signs the checksum file, so no artifact on the release "+
			"page can be verified")
	}
	return problems
}

// buildTargets is every directory a build writes under dist/, including
// the universal one. It is what a glob is held against.
func buildTargets(cfg goreleaserConfig) []buildTarget {
	var targets []buildTarget
	for _, b := range cfg.Builds {
		ignored := map[string]bool{}
		for _, ig := range b.Ignore {
			ignored[ig.Goos+"/"+ig.Goarch] = true
		}
		for _, goos := range b.Goos {
			for _, goarch := range b.Goarch {
				if ignored[goos+"/"+goarch] {
					continue
				}
				binary := b.Binary
				if goos == "windows" {
					binary += ".exe"
				}
				targets = append(targets, buildTarget{
					goos: goos, goarch: goarch, binary: binary,
					dir: b.ID + "_" + goos + "_" + goarch,
				})
			}
		}
	}
	for _, u := range cfg.UniversalBinaries {
		// One binary for both architectures, so goarch is "all" — and
		// the directory is <id>_darwin_all, which is why a glob reading
		// "darwin then universal" matches nothing.
		//
		// The FILE inside it is named by this block's name_template,
		// which defaults to the project name — NOT by the build's
		// `binary`. Taking it from the build is right only while the two
		// happen to be the same string, and a probe with them renamed
		// apart confirmed goreleaser writes the project name (§18 row
		// 71). Reading the wrong one makes this gate agree with a glob
		// that mcpb-pack then cannot resolve.
		targets = append(targets, buildTarget{
			goos: "darwin", goarch: "all", binary: universalName(cfg, u.NameTemplate),
			dir: u.ID + "_darwin_all",
		})
	}
	return targets
}

// matches reports whether a glob's directory pattern names this target.
func (t buildTarget) matches(pattern string) bool {
	ok, err := path.Match(pattern, t.dir)
	return err == nil && ok
}

func targetNames(targets []buildTarget) string {
	var names []string
	for _, t := range targets {
		names = append(names, t.goos+"/"+t.goarch)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// namesFile reports whether any extra_files glob covers the path. The
// config writes `./dist/*.mcpb` and the Makefile writes
// `dist/google-calendar-mcp.mcpb`, so both are cleaned before matching.
func namesFile(files []extraFile, want string) bool {
	want = path.Clean(want)
	for _, f := range files {
		pattern := path.Clean(f.Glob)
		if ok, err := path.Match(pattern, want); err == nil && ok {
			return true
		}
	}
	return false
}

func anySBOMOver(cfg goreleaserConfig, artifacts string) bool {
	for _, s := range cfg.SBOMs {
		if s.Artifacts == artifacts {
			return true
		}
	}
	return false
}

func globCount(files []staged) int {
	n := 0
	for _, f := range files {
		if f.glob != "" {
			n++
		}
	}
	return n
}

func readGoreleaser(p string) (goreleaserConfig, error) {
	data, err := os.ReadFile(p) //nolint:gosec // a repository path from a constant
	if err != nil {
		return goreleaserConfig{}, fmt.Errorf("%s: %w (§12's release does not exist)", p, err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return goreleaserConfig{}, fmt.Errorf("%s is not valid YAML: %w", p, err)
	}
	return cfg, nil
}

// makeVariable reads one `NAME ?= value` from the Makefile, so the path
// the hook packs to has a single owner.
//
// A line scan rather than a regexp built per call: the neighboring gates
// hoist their patterns to package level, and a pattern that has to be
// composed from an argument cannot be.
func makeVariable(p, name string) (string, error) {
	data, err := os.ReadFile(p) //nolint:gosec // a repository path from a constant
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, name)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		rest, ok = strings.CutPrefix(rest, "?=")
		if !ok {
			if rest, ok = strings.CutPrefix(rest, "="); !ok {
				continue
			}
		}
		value, _, _ := strings.Cut(strings.TrimSpace(rest), " #")
		if value = strings.TrimSpace(value); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s defines no %s", p, name)
}

// normalizeVersion reduces the two ways the version is spelled to one,
// so the Makefile's path and the hook's path can be compared as
// patterns. make expands $(VERSION) and goreleaser expands
// {{ .Version }}; neither file can use the other's spelling, and a
// comparison of the literals would fail on two files that agree.
func normalizeVersion(s string) string {
	for _, spelling := range []string{"$(VERSION)", "{{ .Version }}", "{{.Version}}"} {
		s = strings.ReplaceAll(s, spelling, "<version>")
	}
	return s
}

// buildsDarwin reports whether any of the named builds targets darwin.
func buildsDarwin(cfg goreleaserConfig, ids []string) bool {
	for _, b := range cfg.Builds {
		if len(ids) > 0 && !contains(ids, b.ID) {
			continue
		}
		if contains(b.Goos, "darwin") {
			return true
		}
	}
	return false
}

// universalName is the file a universal binary block writes.
//
// goreleaser defaults name_template to the project name, so an empty
// template is not "the same as the build's binary" — it is the project
// name, which is a different field and only sometimes the same string.
func universalName(cfg goreleaserConfig, template string) string {
	if template == "" {
		return cfg.ProjectName
	}
	return strings.NewReplacer(
		"{{ .ProjectName }}", cfg.ProjectName,
		"{{.ProjectName}}", cfg.ProjectName,
	).Replace(template)
}
