package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// transcriptGate parses the live driver's source and fails if any print
// reaches the terminal other than through the redactor.
//
// It exists because redaction that depends on remembering to route a
// call is redaction that ends the next time somebody adds a debug line —
// the new print looks exactly like the safe ones beside it. A sibling
// found a section header printing directly, one refactor away from
// carrying a document title with it.
//
// The driver talks to a real account, so what it prints is somebody's
// real calendar.
func transcriptGate() error {
	files, err := driverFiles()
	if err != nil {
		return err
	}
	// The floor: this gate is worthless if it scanned nothing, and a
	// build tag or a renamed directory would make that silent.
	if len(files) < 2 {
		return fmt.Errorf("found %d live-driver files; the gate is not reading the driver", len(files))
	}

	var problems []string
	checked := 0

	for _, path := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := calleeName(call)
			if !ok {
				return true
			}
			checked++
			if reachesTerminal(name, call) {
				pos := fset.Position(call.Pos())
				problems = append(problems, fmt.Sprintf(
					"%s:%d: %s writes to the terminal directly; route it through the redact.Printer",
					path, pos.Line, name))
			}
			return true
		})
	}

	if checked < 20 {
		return fmt.Errorf("inspected only %d calls across the driver; that is not the whole thing", checked)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d files, %d calls: every print goes through the redactor\n", len(files), checked)
	return nil
}

// driverFiles are the packages that talk to a real account.
func driverFiles() ([]string, error) {
	var out []string
	// scripts/spikes was in this list and never existed: the live probes
	// are in scripts/livecal (§18 row 46). It was invisible because a
	// missing directory was skipped, so the list could name anything.
	// A directory this gate is told to read and cannot find fails it
	// now — the floor below counts files, and scripts/livecal alone
	// clears it, so losing scripts/evals would otherwise be silent.
	for _, dir := range []string{"scripts/livecal", "scripts/evals"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read %s, which this gate is told to scan: %w", dir, err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".go") {
				out = append(out, filepath.ToSlash(filepath.Join(dir, e.Name())))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// calleeName renders the called function as written.
func calleeName(call *ast.CallExpr) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name, true
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name + "." + fn.Sel.Name, true
		}
		return fn.Sel.Name, true
	}
	return "", false
}

// reachesTerminal reports whether this call can put bytes in front of a
// person without passing the redactor.
//
// The distinction that matters is the DESTINATION, not the function.
// fmt.Print* always reaches stdout, so it is forbidden outright. But
// fmt.Fprintf is how the driver writes JSON-RPC frames into the server's
// stdin, which is a pipe and not a transcript — the first version of
// this gate forbade the function and flagged those two lines, which
// would have taught the next person to disable the check rather than
// trust it.
//
// fmt.Errorf and fmt.Sprintf are absent deliberately: they produce a
// value, and whoever prints it does so through the Printer.
func reachesTerminal(name string, call *ast.CallExpr) bool {
	switch name {
	case "fmt.Print", "fmt.Printf", "fmt.Println", "print", "println":
		return true
	case "fmt.Fprint", "fmt.Fprintf", "fmt.Fprintln":
		return len(call.Args) > 0 && isTerminalWriter(call.Args[0])
	}
	// A logger reaches stderr, and stderr is where the transcript is.
	return strings.HasPrefix(name, "log.") || strings.HasPrefix(name, "slog.")
}

// isTerminalWriter reports whether an expression names a process stream.
func isTerminalWriter(arg ast.Expr) bool {
	sel, ok := arg.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "os" {
		return false
	}
	return sel.Sel.Name == "Stdout" || sel.Sel.Name == "Stderr"
}
