package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Break the release config every way it breaks and watch each be
// refused, for the same reason the bundle gate does it: a check nobody
// has watched fail is a check nobody knows the shape of.
//
// The first case is not hypothetical. It is the glob phase 4 committed,
// and every gate was green with it in the tree because the packer only
// runs at release time and there was no release.

// goodWorkflow is a release workflow with nothing wrong with it, so a
// case testing the config is not also failing on the workflow.
const goodWorkflow = `name: release
on:
  push:
    tags: ['v*.*.*']
jobs:
  goreleaser:
    steps:
      - run: go run ./scripts/gates release-notes "${REF_NAME}" > notes.md
      - uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94
        with:
          args: release --clean --release-notes=notes.md
      - uses: actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8
        with:
          subject-path: "dist/*.tar.gz,dist/*.zip,dist/*.mcpb,dist/checksums.txt"
`

// goodOut is the bundle path the Makefile owns, read rather than
// repeated: a copy here would let the two drift and still pass.
func goodOut(t *testing.T) string {
	t.Helper()
	out, err := makeVariable(repoPath(t, makefilePath), "MCPB_OUT")
	if err != nil {
		t.Fatalf("read MCPB_OUT: %v", err)
	}
	return out
}

// goodConfig is the committed config, which every case starts from.
func goodConfig(t *testing.T) goreleaserConfig {
	t.Helper()
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(repoFile(t, goreleaserPath), &cfg); err != nil {
		t.Fatalf("the committed %s is not valid YAML: %v", goreleaserPath, err)
	}
	return cfg
}

func TestTheCommittedReleaseConfigIsValid(t *testing.T) {
	cfg := goodConfig(t)
	flow := asWorkflow(t, string(repoFile(t, releaseWorkflowPath)))
	problems, targets := validateRelease(cfg, bundleFiles, goodOut(t), flow)
	if len(problems) > 0 {
		t.Fatalf("the committed release wiring does not describe the bundle it ships:\n%s",
			strings.Join(problems, "\n"))
	}
	if len(targets) < 6 {
		t.Fatalf("read %d build targets from the committed config", len(targets))
	}
}

// The build matrix has to produce the six platform archives §12 claims,
// plus the universal binary the bundle's macOS slot is.
func TestTheBuildMatrixIsTheOneDocumented(t *testing.T) {
	targets := buildTargets(goodConfig(t))
	want := map[string]bool{
		"linux/amd64": true, "linux/arm64": true,
		"darwin/amd64": true, "darwin/arm64": true,
		"windows/amd64": true, "windows/arm64": true,
		"darwin/all": true,
	}
	got := map[string]bool{}
	for _, tg := range targets {
		got[tg.goos+"/"+tg.goarch] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("the build matrix does not produce %s", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("the build matrix produces %s, which nothing documents", name)
		}
	}
}

func TestTheWaysTheReleaseWiringBreaks(t *testing.T) {
	cases := []struct {
		name   string
		breaks func(cfg *goreleaserConfig, files *[]staged, out *string, workflow *string)
		want   string
	}{
		{
			// Phase 4's actual glob. goreleaser names the directory
			// <id>_darwin_all, so the id comes first.
			name: "the universal glob written the obvious way round",
			breaks: func(_ *goreleaserConfig, files *[]staged, _ *string, _ *string) {
				setGlob(files, "server/google-calendar-mcp-darwin",
					"dist/*darwin*universal*/google-calendar-mcp")
			},
			want: "matches no directory",
		},
		{
			name: "a glob that matches both macOS architectures",
			breaks: func(_ *goreleaserConfig, files *[]staged, _ *string, _ *string) {
				setGlob(files, "server/google-calendar-mcp-darwin", "dist/*darwin*/google-calendar-mcp")
			},
			want: "must name exactly one",
		},
		{
			name: "a Windows glob that forgets the extension",
			breaks: func(_ *goreleaserConfig, files *[]staged, _ *string, _ *string) {
				setGlob(files, "server/google-calendar-mcp.exe", "dist/*windows_amd64*/google-calendar-mcp")
			},
			want: "writes \"google-calendar-mcp.exe\"",
		},
		{
			name: "a platform spawning another platform's directory",
			breaks: func(_ *goreleaserConfig, files *[]staged, _ *string, _ *string) {
				setGlob(files, "server/google-calendar-mcp.exe", "dist/*linux_amd64*/google-calendar-mcp")
			},
			want: "staged for the manifest platform",
		},
		{
			name: "a build matrix that drops an architecture the launcher picks",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Builds[0].Goarch = []string{"amd64"}
			},
			want: "matches no directory",
		},
		{
			// lipo over nothing. The macOS glob still resolves, because
			// the universal directory comes from this block rather than
			// from the matrix, so only this names it.
			name: "a universal binary with no darwin build to join",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Builds[0].Goos = []string{"linux", "windows"}
			},
			want: "nothing for it to join",
		},
		{
			name: "nothing packing the bundle",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.UniversalBinaries[0].Hooks.Post = ""
			},
			want: "no post hook",
		},
		{
			name: "a hook packing where the Makefile does not look",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.UniversalBinaries[0].Hooks.Post = "go run ./scripts/gates mcpb-pack dist 1.0.0 dist/bundle.mcpb"
			},
			want: "the Makefile does not name",
		},
		{
			// Hashed and never published, or published and never hashed.
			// Neither looks any different on the release page.
			name: "a bundle nobody checksums",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Checksum.ExtraFiles = nil
			},
			want: "not under the signature",
		},
		{
			name: "a bundle nobody uploads",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Release.ExtraFiles = nil
			},
			want: "hashed and never uploaded",
		},
		{
			name: "archives that pick up the universal binary too",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Archives[0].IDs = nil
			},
			want: "names no ids",
		},
		{
			name: "a universal binary that replaces the macOS archives",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.UniversalBinaries[0].Replace = true
			},
			want: "removes the per-architecture",
		},
		{
			name: "a build that carries this machine's paths",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Builds[0].Flags = nil
			},
			want: "-trimpath",
		},
		{
			name: "a build whose timestamp is the build's, not the commit's",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Builds[0].ModTimestamp = ""
			},
			want: "does not reproduce it",
		},
		{
			name: "no SBOM beside the archives",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.SBOMs = nil
			},
			want: "no sboms block",
		},
		{
			name: "nothing signing the checksum file",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Signs = nil
			},
			want: "nothing signs the checksum file",
		},
		{
			// cosign 3 made --bundle required. With only the version-2
			// flags it is given no output path at all.
			name: "a signature written the way cosign 2 wanted",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Signs[0].Args = []string{"sign-blob", "--output-signature=${signature}", "--yes", "${artifact}"}
			},
			want: "--bundle",
		},
		{
			// The trap three servers wrote down: it reads like the way to
			// leave --release-notes in charge and is the opposite.
			name: "a changelog block, which collapses the body to the footer",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Changelog = &yaml.Node{Kind: yaml.MappingNode}
			},
			want: "Deleting the block",
		},
		{
			name: "a release that publishes a draft nobody presses publish on",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Release.Draft = true
			},
			want: "remembers to press a button",
		},
		{
			name: "a signature written by something other than cosign",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Signs[0].Cmd = "gpg"
			},
			want: "rather than cosign",
		},
		{
			name: "a glob that hardcodes goreleaser's variant suffix",
			breaks: func(_ *goreleaserConfig, files *[]staged, _ *string, _ *string) {
				setGlob(files, "server/google-calendar-mcp-linux-x64", "dist/*linux_amd64_v1/google-calendar-mcp")
			},
			want: "matches no directory",
		},
		{
			name: "a bundle the provenance does not cover",
			breaks: func(_ *goreleaserConfig, _ *[]staged, _ *string, workflow *string) {
				*workflow = strings.ReplaceAll(*workflow, "dist/*.mcpb,", "")
			},
			want: "does not cover the bundle",
		},
		{
			name: "a checksum file the provenance does not name",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Checksum.NameTemplate = "SHA256SUMS"
			},
			want: "verified through carries no attestation",
		},
		{
			name: "a packer staging too few files to be a bundle",
			breaks: func(_ *goreleaserConfig, files *[]staged, _ *string, _ *string) {
				*files = (*files)[:2]
			},
			want: "that is not the bundle",
		},
		{
			// Probed: with the two renamed apart, goreleaser writes the
			// PROJECT name into the universal directory and the build's
			// `binary` into every other one. So changing the project name
			// alone moves the macOS file and nothing else — which the
			// first version of this gate could not see, because it took
			// that name from the build.
			name: "a project renamed out from under the macOS slot",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.ProjectName = "calendar-mcp"
			},
			want: "darwin/all writes \"calendar-mcp\"",
		},
		{
			// And the other half: the build's binary names every
			// directory except the universal one.
			name: "a build binary renamed out from under the rest",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Builds[0].Binary = "gcal-probe"
			},
			want: "writes \"gcal-probe\"",
		},
		{
			name: "a matrix that ignores a platform the release page claims",
			breaks: func(cfg *goreleaserConfig, _ *[]staged, _ *string, _ *string) {
				cfg.Builds[0].Ignore = append(cfg.Builds[0].Ignore, struct {
					Goos   string `yaml:"goos"`
					Goarch string `yaml:"goarch"`
				}{Goos: "windows", Goarch: "arm64"})
			},
			want: "§12 says six archives",
		},
		{
			name: "a workflow that lets goreleaser write the notes",
			breaks: func(_ *goreleaserConfig, _ *[]staged, _ *string, workflow *string) {
				*workflow = strings.ReplaceAll(*workflow, "--release-notes", "--rm-dist")
			},
			want: "generated from commit subjects",
		},
		{
			name: "a release nothing but a human can start",
			breaks: func(_ *goreleaserConfig, _ *[]staged, _ *string, workflow *string) {
				*workflow = strings.ReplaceAll(*workflow, "tags: ['v*.*.*']", "branches: [main]")
			},
			want: "does not trigger on a v* tag",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := goodConfig(t)
			files := append([]staged(nil), bundleFiles...)
			out := goodOut(t)
			workflow := goodWorkflow
			tc.breaks(&cfg, &files, &out, &workflow)

			problems, _ := validateRelease(cfg, files, out, asWorkflow(t, workflow))
			if len(problems) == 0 {
				t.Fatalf("broke the release wiring (%s) and the gate passed", tc.name)
			}
			// mentions rather than a joined string: a `want` that
			// straddled two unrelated problems would pass for the wrong
			// reason.
			if !mentions(problems, tc.want) {
				t.Fatalf("wanted a problem mentioning %q, got:\n%s", tc.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// setGlob replaces one staged file's glob, leaving the rest alone.
func setGlob(files *[]staged, path, glob string) {
	for i := range *files {
		if (*files)[i].path == path {
			(*files)[i].glob = glob
			return
		}
	}
	panic("no staged file at " + path)
}
