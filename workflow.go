package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

const filePermOwnerRW = 0o600

// workflow is a YAML file along with the uses: lines that should be pinned.
type workflow struct {
	path  string
	lines []string
	uses  []usesLine
}

type usesLine struct {
	idx      int
	prefix   string
	repoPath string
	quote    string
	target   target
}

func loadWorkflow(path string, cfg config, stderr io.Writer) (*workflow, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	wf := &workflow{path: path, lines: lines}
	for i, line := range lines {
		if use, ok := lineToUses(i, line, cfg); ok {
			wf.uses = append(wf.uses, use)
		} else if cfg.verbose && isLocalUses(line) {
			_, _ = fmt.Fprintf(stderr, "skipping %s:%d: %s\n", path, i+1, strings.TrimSpace(line))
		}
	}
	return wf, nil
}

func lineToUses(idx int, line string, cfg config) (usesLine, bool) {
	directive, skip, local := parseUsesLine(line)
	if skip || local || (isSHA(directive.ref) && !cfg.update) {
		return usesLine{}, false
	}
	owner, repo := splitOwnerRepo(directive.repoPath)
	t := target{owner: owner, repo: repo, ref: directive.ref}
	if cfg.update {
		t.ref = latestRef
	}
	return usesLine{
		idx:      idx,
		prefix:   directive.prefix,
		repoPath: directive.repoPath,
		quote:    directive.quote,
		target:   t,
	}, true
}

func isLocalUses(line string) bool {
	_, _, local := parseUsesLine(line)
	return local
}

func (w *workflow) targets() []target {
	targets := make([]target, len(w.uses))
	for i, use := range w.uses {
		targets[i] = use.target
	}
	return targets
}

// pin rewrites the uses: lines with their resolved versions and reports whether
// any line changed. Lines of unresolved actions stay as they are.
func (w *workflow) pin(resolved map[target]resolvedRef) bool {
	changed := false
	for _, use := range w.uses {
		ref, ok := resolved[use.target]
		if !ok {
			continue
		}
		line := use.prefix + use.quote + use.repoPath + "@" + ref.sha + use.quote + " # " + ref.version
		if line != w.lines[use.idx] {
			w.lines[use.idx] = line
			changed = true
		}
	}
	return changed
}

// write saves the workflow in place when it changed, or prints it to stdout. A
// header tells the files apart when multiple files are printed.
func (w *workflow) write(stdout io.Writer, changed, inPlace, withHeader bool) error {
	content := strings.Join(w.lines, "\n") + "\n"
	if inPlace {
		if !changed {
			return nil
		}
		if err := os.WriteFile(w.path, []byte(content), filePermOwnerRW); err != nil {
			return fmt.Errorf("writing %q: %w", w.path, err)
		}
		return nil
	}
	if withHeader {
		content = "# " + w.path + "\n" + content
	}
	if _, err := io.WriteString(stdout, content); err != nil {
		return fmt.Errorf("writing %q: %w", w.path, err)
	}
	return nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: reading the files the user names is the purpose of this tool.
	if err != nil {
		return nil, fmt.Errorf("reading workflow: %w", err)
	}

	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %q: %w", path, err)
	}
	return lines, nil
}
