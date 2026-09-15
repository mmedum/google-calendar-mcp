package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
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
}

// verdict is one hand-written row of testdata/api-coverage.tsv.
type verdict struct {
	api, method, verdict, reason string
	line                         int
}

// apiCoverageGate holds three things at once, offline:
//
//  1. a published method with no verdict fails;
//  2. a verdict for a method that no longer exists fails;
//  3. a verdict of `used` with no reason, or `out` with no reason, fails.
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
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(bad)

	var problems []string
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
	f, err := os.Open("testdata/api-coverage.tsv")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []verdict
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			return nil, fmt.Errorf("line %d is not four tab-separated fields: %q", n, line)
		}
		out = append(out, verdict{
			api: parts[0], method: parts[1], verdict: parts[2],
			reason: strings.Join(parts[3:], "\t"), line: n,
		})
	}
	return out, sc.Err()
}
