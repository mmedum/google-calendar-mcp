package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// liveCoverGate holds every published tool and resource to having a
// live step.
//
// Anything touching the write path or a response shape counts only after
// a live run whose transcript is read (D-70). That holds only for the
// tools the driver actually calls, and a tool added later is exactly the
// one nobody remembers to drive — the fake answers it, every test passes,
// and the first real call is a user's.
//
// Both directions, like the other records:
//
//  1. a published tool with no step fails, unless the driver names it in
//     liveUncovered with a reason;
//  2. a step for a tool that no longer exists fails, which is what a
//     rename leaves behind.
func liveCoverGate(bin string) error {
	published, err := dumpFrom(bin)
	if err != nil {
		return err
	}
	if len(published) < 5 {
		return fmt.Errorf("the binary published %d tools; that is not the surface", len(published))
	}

	driven, exempt, steps, err := driverTools()
	if err != nil {
		return err
	}
	// Floors: a driver that calls two tools would otherwise pass this
	// gate by calling them in ten steps.
	if steps < 10 {
		return fmt.Errorf("the driver has %d steps; that is not a run worth reading", steps)
	}
	if len(driven) < 5 {
		return fmt.Errorf("the driver calls %d distinct tools; the gate is not reading the steps", len(driven))
	}

	var missing, stale, unexplained []string
	for name := range published {
		if driven[name] {
			continue
		}
		reason, named := exempt[name]
		switch {
		case !named:
			missing = append(missing, name)
		case strings.TrimSpace(reason) == "":
			unexplained = append(unexplained, name)
		}
	}
	for name := range driven {
		if _, ok := published[name]; !ok {
			stale = append(stale, name)
		}
	}
	for name := range exempt {
		if driven[name] {
			stale = append(stale, name+" (listed in liveUncovered and driven anyway; remove the entry)")
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(unexplained)

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "tools the live driver never calls:\n  "+strings.Join(missing, "\n  ")+
			"\n  (add a step in "+liveDriverPackage+", or name it in liveUncovered with why it cannot be driven)")
	}
	if len(unexplained) > 0 {
		problems = append(problems, "tools in liveUncovered with no reason:\n  "+strings.Join(unexplained, "\n  "))
	}
	if len(stale) > 0 {
		problems = append(problems, "driver steps for tools the binary does not publish:\n  "+strings.Join(stale, "\n  "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n\n"))
	}

	fmt.Printf("  %d published tools and resources, %d steps: %d driven live, %d exempt with a reason\n",
		len(published), steps, len(driven), len(exempt))
	return nil
}

// driverTools reads the live driver's source for the tools its steps
// call, the exemptions it declares, and how many steps there are.
//
// It parses rather than greps for the same reason the transcript gate
// does: a string in a comment is not a call, and a gate that cannot tell
// the difference is one that passes on a commented-out step.
func driverTools() (driven map[string]bool, exempt map[string]string, steps int, err error) {
	files, err := driverFiles()
	if err != nil {
		return nil, nil, 0, err
	}
	driven, exempt = map[string]bool{}, map[string]string{}

	for _, path := range files {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil, nil, 0, fmt.Errorf("parse %s: %w", path, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				// A step literal: { name: ..., tool: "list_events", ... }
				for _, el := range node.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					lit, _ := stringOf(kv.Value)
					if lit == "" {
						continue
					}
					switch key.Name {
					case "tool":
						driven[lit] = true
						steps++
					case "resource":
						// A resource step names the published URI or
						// template, which is the key the dump uses, so
						// a resource nobody reads live fails this gate
						// exactly as an undriven tool does.
						driven[resourceKey(lit)] = true
						steps++
					}
				}
			case *ast.ValueSpec:
				if len(node.Names) == 0 || node.Names[0].Name != "liveUncovered" {
					return true
				}
				for _, v := range node.Values {
					cl, ok := v.(*ast.CompositeLit)
					if !ok {
						continue
					}
					for _, el := range cl.Elts {
						kv, ok := el.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						name, _ := stringOf(kv.Key)
						exempt[name], _ = stringOf(kv.Value)
					}
				}
			}
			return true
		})
	}
	return driven, exempt, steps, nil
}

// stringOf is the value of a string literal, and whether e is one.
func stringOf(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}
