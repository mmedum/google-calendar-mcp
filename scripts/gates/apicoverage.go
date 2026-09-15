package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// apiSurface is testdata/api-surface.json: machine-owned, written by
// api-diff, edited by nobody.
type apiSurface struct {
	Fetched string `json:"fetched"`
	APIs    []struct {
		API      string `json:"api"`
		Version  string `json:"version"`
		Revision string `json:"revision"`
		URL      string `json:"url"`
	} `json:"apis"`
	Methods []struct {
		Name string `json:"name"`
		Verb string `json:"verb"`
		Path string `json:"path"`
	} `json:"methods"`
	// Schemas is the field-level half (§8b). Methods are the coarse
	// axis; this API keeps its capability in the Event resource, and a
	// method-only record hides the 44 properties nobody considered.
	Schemas []schemaRow `json:"schemas"`
}

// schemaRow is one published resource and its properties.
type schemaRow struct {
	Resource string   `json:"resource"`
	Fields   []string `json:"fields"`
}

// verdict is one hand-written row of testdata/api-coverage.tsv.
type verdict struct {
	api, method, verdict, reason string
	line                         int
}

// apiCoverageGate holds four things at once, offline:
//
//  1. a published method with no verdict fails;
//  2. a verdict for a method that no longer exists fails;
//  3. a verdict of `used` with no reason, or `out` with no reason, fails;
//  4. a client method in internal/gapi that no row names fails.
//
// The fourth is the one this file's header and CLAUDE.md rule 11 both
// promised and nothing implemented, for a whole phase. A record that
// only reads the published surface can be complete while the code calls
// something nobody decided about.
//
// The verb and path live ONLY in the machine-owned snapshot. A sibling
// put them in the hand-written file too, and ended up with 112 rows of
// two fields nothing ever read — so a method that kept its name and
// changed its path was invisible.
func apiCoverageGate() error {
	surface, err := loadSurface()
	if err != nil {
		return err
	}
	verdicts, err := loadVerdicts()
	if err != nil {
		return err
	}

	// Floors.
	if len(surface.Methods) < 20 {
		return fmt.Errorf("the API snapshot lists only %d methods; that is not the published surface", len(surface.Methods))
	}
	if len(verdicts) < 20 {
		return fmt.Errorf("only %d verdicts; that is not a real coverage record", len(verdicts))
	}

	published := map[string]bool{}
	for _, m := range surface.Methods {
		published[m.Name] = true
	}
	recorded := map[string]verdict{}
	for _, v := range verdicts {
		if prev, dup := recorded[v.method]; dup {
			return fmt.Errorf("%s has two verdicts, on lines %d and %d", v.method, prev.line, v.line)
		}
		recorded[v.method] = v
	}

	var missing, stale, bad []string
	for name := range published {
		if _, ok := recorded[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name, v := range recorded {
		if !published[name] {
			stale = append(stale, fmt.Sprintf("%s (line %d)", name, v.line))
		}
		switch v.verdict {
		case "used", "out":
			if strings.TrimSpace(v.reason) == "" {
				bad = append(bad, fmt.Sprintf("%s (line %d): verdict %q with no reason", name, v.line, v.verdict))
			}
		default:
			bad = append(bad, fmt.Sprintf("%s (line %d): verdict %q is neither used nor out", name, v.line, v.verdict))
		}
	}
	unnamed, err := unnamedClientMethods(recorded)
	if err != nil {
		return err
	}

	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(bad)
	sort.Strings(unnamed)

	var problems []string
	if len(unnamed) > 0 {
		problems = append(problems, "client methods in internal/gapi that no verdict names:\n  "+
			strings.Join(unnamed, "\n  ")+
			"\n  (this server calls it and the record does not say which published method it implements. "+
			"Name it in the reason column, as the other rows do.)")
	}
	if len(missing) > 0 {
		problems = append(problems, "published methods with no verdict in testdata/api-coverage.tsv:\n  "+
			strings.Join(missing, "\n  ")+
			"\n  (Google published a capability and nobody decided about it. Add a row saying used or out, with why.)")
	}
	if len(stale) > 0 {
		problems = append(problems, "verdicts for methods the API no longer publishes:\n  "+strings.Join(stale, "\n  "))
	}
	if len(bad) > 0 {
		problems = append(problems, "malformed verdicts:\n  "+strings.Join(bad, "\n  "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n\n"))
	}

	used, out := 0, 0
	for _, v := range recorded {
		if v.verdict == "used" {
			used++
		} else {
			out++
		}
	}
	rev := "unknown"
	if len(surface.APIs) > 0 {
		rev = surface.APIs[0].Revision
	}
	fmt.Printf("  %d methods (revision %s, fetched %s): %d used, %d written off\n",
		len(published), rev, surface.Fetched, used, out)
	return nil
}

// unnamedClientMethods returns the exported methods on gapi.Client that
// no verdict's reason mentions.
//
// The reasons name their implementation as `gapi.ListEvents`, so this
// reads the client's own source and looks for each name. A method added
// to the client without a row is a call this server makes that nobody
// wrote a verdict for.
func unnamedClientMethods(recorded map[string]verdict) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("internal", "gapi", "client.go"), nil, 0)
	if err != nil {
		return nil, err
	}

	named := map[string]bool{}
	for _, v := range recorded {
		for _, word := range strings.Fields(strings.ReplaceAll(v.reason, ",", " ")) {
			if rest, ok := strings.CutPrefix(word, "gapi."); ok {
				named[rest] = true
			}
		}
	}

	var out []string
	methods := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || !fn.Name.IsExported() {
			continue
		}
		if !receiverIs(fn, "Client") {
			continue
		}
		methods++
		if !named[fn.Name.Name] {
			out = append(out, "gapi."+fn.Name.Name)
		}
	}
	// A floor, as every gate here has one: no methods found means the
	// parser stopped matching, not that the client is empty.
	if methods < 5 {
		return nil, fmt.Errorf("found %d exported methods on gapi.Client; the parser is not reading the client", methods)
	}
	return out, nil
}

func receiverIs(fn *ast.FuncDecl, typeName string) bool {
	if len(fn.Recv.List) == 0 {
		return false
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == typeName
}

func loadSurface() (*apiSurface, error) {
	data, err := os.ReadFile("testdata/api-surface.json")
	if err != nil {
		return nil, fmt.Errorf("read the API snapshot: %w (run `make api-diff`)", err)
	}
	var s apiSurface
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse the API snapshot: %w", err)
	}
	return &s, nil
}

func loadVerdicts() ([]verdict, error) {
	rows, err := loadTSV("testdata/api-coverage.tsv", 4)
	if err != nil {
		return nil, err
	}
	out := make([]verdict, 0, len(rows))
	for _, r := range rows {
		out = append(out, verdict{
			api: r.fields[0], method: r.fields[1], verdict: r.fields[2],
			reason: r.fields[3], line: r.line,
		})
	}
	return out, nil
}

// tsvRow is one record line: the leading columns split out, and
// everything after them joined back as the reason.
type tsvRow struct {
	fields []string
	line   int
}

// loadTSV reads one of the repository's hand-written record files.
//
// Both records are the same shape — columns, then a reason that may
// contain anything including tabs — and they had a reader each. The
// second one grew a duplicate check the first did not have, which is
// how two copies of a loader stop being two copies of the same loader.
func loadTSV(path string, columns int) ([]tsvRow, error) {
	f, err := os.Open(path) //nolint:gosec // a record file named by a constant in this package
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []tsvRow
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < columns {
			return nil, fmt.Errorf("%s line %d is not %d tab-separated fields: %q", path, n, columns, line)
		}
		// The last column is the reason, and a reason may contain tabs.
		fields := append([]string{}, parts[:columns-1]...)
		fields = append(fields, strings.Join(parts[columns-1:], "\t"))
		out = append(out, tsvRow{fields: fields, line: n})
	}
	return out, sc.Err()
}
