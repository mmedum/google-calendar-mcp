package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// One reader for a GitHub workflow, shared by every gate that asks a
// question about one.
//
// It replaced a hand-rolled step splitter, which was wrong in both of the
// ways a hand-rolled one is. It read every line of a step rather than the
// `with:` block, so a `version:` under `env:` satisfied a tool pin — and
// `version` is the most collidable input name there is. And it understood
// only block sequences, so a workflow written any other way produced no
// steps at all and therefore no problems: "looked at nothing" printing
// the sentence "found nothing", which is the one failure every gate here
// is written to refuse.
//
// Both were found by probing the code rather than reading it, which is
// the argument for this file existing rather than for that one being
// corrected.

// workflow is the part of a workflow file the gates are about.
type workflow struct {
	// path is where it was read from, for a message that names the file.
	path string
	// On is left as a node: `on` is a YAML 1.1 boolean, and the triggers
	// are a union of shapes rather than one type.
	On   yaml.Node `yaml:"on"`
	Jobs map[string]struct {
		Steps []workflowStep `yaml:"steps"`
	} `yaml:"jobs"`
}

// workflowStep is one entry under a job's `steps:`.
type workflowStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
}

// input reads one `with:` value as text.
//
// The values are not all strings — `fetch-depth: 0` is an int — so they
// are rendered rather than type-asserted, which is also what makes a
// version written unquoted behave like one written in quotes.
func (s workflowStep) input(key string) (string, bool) {
	v, ok := s.With[key]
	if !ok {
		return "", false
	}
	return strings.TrimSpace(fmt.Sprint(v)), true
}

// steps is every step in the file, jobs in name order so a failure lists
// them the same way twice.
func (w workflow) steps() []workflowStep {
	names := make([]string, 0, len(w.Jobs))
	for name := range w.Jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []workflowStep
	for _, name := range names {
		out = append(out, w.Jobs[name].Steps...)
	}
	return out
}

// triggersOnTag reports whether a push of a tag matching prefix starts
// this workflow. A workflow only a person can start is one nobody
// remembers to start.
func (w workflow) triggersOnTag(prefix string) bool {
	push := mappingValue(&w.On, "push")
	if push == nil {
		return false
	}
	tags := mappingValue(push, "tags")
	if tags == nil {
		return false
	}
	for _, pattern := range tags.Content {
		if strings.HasPrefix(pattern.Value, prefix) {
			return true
		}
	}
	// `tags: v*` rather than a list.
	return strings.HasPrefix(tags.Value, prefix)
}

// mappingValue reads one key out of a YAML mapping node.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// readWorkflow parses one workflow file.
func readWorkflow(path string) (workflow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a path under .github/workflows
	if err != nil {
		return workflow{}, err
	}
	var w workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return workflow{}, fmt.Errorf("%s is not valid YAML: %w", path, err)
	}
	w.path = path
	return w, nil
}

// workflowPaths is every workflow in the repository, sorted.
func workflowPaths() ([]string, error) {
	files, err := filepath.Glob(".github/workflows/*.yml")
	if err != nil {
		return nil, err
	}
	more, err := filepath.Glob(".github/workflows/*.yaml")
	if err != nil {
		return nil, err
	}
	files = append(files, more...)
	// Forward slashes on every platform. filepath.Glob returns the
	// separator the OS uses, and the callers compare against literals
	// written with slashes — so on Windows a required workflow was
	// reported missing from a list that plainly contained it.
	for i, f := range files {
		files[i] = filepath.ToSlash(f)
	}
	sort.Strings(files)
	return files, nil
}
