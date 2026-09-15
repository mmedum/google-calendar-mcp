// Package version exposes the build-time version string. Set with
// -ldflags "-X github.com/mmedum/google-calendar-mcp/internal/version.Version=v1.2.3".
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Version is the semantic version of the binary. Releases set it through
// ldflags; a local build leaves it "dev".
var Version = "dev"

// String is the version to report. `go install module@v1.2.3` applies no
// ldflags, so a binary installed the way the README suggests would call
// itself "dev" forever. Go records the module version it was built from,
// which is the honest answer in that case.
func String() string {
	if Version != "dev" {
		return canonical(Version)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return canonical(v)
		}
	}
	return Version
}

// canonical is the one spelling of a release, whichever way the binary
// was built. goreleaser stamps {{.Version}} with the leading v stripped;
// `go install` stamps nothing and the build-info fallback reads the
// module version, which keeps its v. Without this the same release
// reports two different strings depending on how it was installed.
func canonical(v string) string {
	if v == "" || v == "dev" {
		return v
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// Info is the one-line description --version prints.
func Info() string {
	return fmt.Sprintf("google-calendar-mcp %s (%s %s/%s)", String(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
