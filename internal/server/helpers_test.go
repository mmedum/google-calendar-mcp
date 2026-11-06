package server_test

import (
	"runtime/debug"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// linkedVersion is the version of a module the test binary links, a
// replacement's when go.mod replaces it. A replacement by a local path has
// no version, so the test skips.
func linkedVersion(t *testing.T, module string) string {
	t.Helper()
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("the test binary carries no build info")
	}
	for _, dep := range info.Deps {
		if dep.Path != module {
			continue
		}
		if dep.Replace == nil {
			return dep.Version
		}
		// The go command records a path replacement's missing version as
		// (devel) (https://github.com/golang/go/blob/go1.27.3/src/cmd/go/internal/load/pkg.go#L2381).
		if dep.Replace.Version == "(devel)" {
			t.Skipf("go.mod replaces %s with the path %s, which has no version to compare", module, dep.Replace.Path)
		}
		return dep.Replace.Version
	}
	t.Fatalf("the test binary links no %s; this test is asserting on nothing", module)
	return ""
}

func newTransports() (mcp.Transport, mcp.Transport) { return mcp.NewInMemoryTransports() }

func newClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
}
