package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// coverageGate enforces a floor PER PACKAGE, not on the average.
//
// An average hides the package that matters: a repository at 85% overall
// can have the one package carrying a confidentiality guarantee at 30%.
func coverageGate(profile string, minPct float64) error {
	f, err := os.Open(profile)
	if err != nil {
		return fmt.Errorf("open %s: %w (run `make test` first)", profile, err)
	}
	defer func() { _ = f.Close() }()

	// Deduplicate by BLOCK, not by line.
	//
	// `go test -coverpkg=./...` makes every test binary emit a profile
	// for every package, so the merged file lists each block once per
	// test binary — here, once per package with tests. Summing the lines
	// counts the same statements ten times over and reports a package
	// with real tests at a tenth of its coverage. A block is covered if
	// ANY run covered it, which is what `go tool cover` itself does.
	type block struct {
		statements int
		covered    bool
	}
	blocks := map[string]*block{}
	pkgOf := map[string]string{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	lines := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		lines++
		// file.go:1.2,3.4 numStatements count
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		file := line[:colon]
		slash := strings.LastIndex(file, "/")
		if slash < 0 {
			continue
		}
		pkg := file[:slash]

		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			continue
		}
		n, err1 := strconv.Atoi(fields[1])
		c, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		key := file + ":" + fields[0]
		b := blocks[key]
		if b == nil {
			b = &block{statements: n}
			blocks[key] = b
			pkgOf[key] = pkg
		}
		if c > 0 {
			b.covered = true
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}

	type counter struct{ covered, total int }
	pkgs := map[string]*counter{}
	for key, b := range blocks {
		pkg := pkgOf[key]
		if pkgs[pkg] == nil {
			pkgs[pkg] = &counter{}
		}
		pkgs[pkg].total += b.statements
		if b.covered {
			pkgs[pkg].covered += b.statements
		}
	}

	// The floor on how much was read. Without it an empty or truncated
	// profile passes silently, which is the failure mode this whole
	// gate family is written against.
	if lines < 100 {
		return fmt.Errorf("coverage profile has only %d statement lines; that is not a real run", lines)
	}
	if len(pkgs) < 5 {
		return fmt.Errorf("coverage profile covers only %d packages; expected the whole module", len(pkgs))
	}

	var names []string
	for p := range pkgs {
		names = append(names, p)
	}
	sort.Strings(names)

	var below []string
	for _, p := range names {
		c := pkgs[p]
		if c.total == 0 {
			continue
		}
		pct := 100 * float64(c.covered) / float64(c.total)
		if pct < minPct {
			below = append(below, fmt.Sprintf("  %-55s %5.1f%%", p, pct))
		}
	}
	if len(below) > 0 {
		return fmt.Errorf("these packages are below the %.0f%% floor:\n%s", minPct, strings.Join(below, "\n"))
	}
	fmt.Printf("  %d packages, %d distinct blocks, all at or above %.0f%%\n", len(pkgs), len(blocks), minPct)
	return nil
}
