package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

var errNoYAMLFiles = errors.New("no .yml or .yaml files")

// collectFiles expands args into a deduplicated list of file paths. Files are
// taken as given, directories are searched recursively for YAML files.
func collectFiles(args []string) ([]string, error) {
	var paths []string
	seen := make(map[string]struct{})
	for _, arg := range args {
		found, err := expandArg(arg)
		if err != nil {
			return nil, err
		}
		for _, path := range found {
			path = filepath.Clean(path)
			if _, dup := seen[path]; !dup {
				seen[path] = struct{}{}
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

func expandArg(arg string) ([]string, error) {
	info, err := os.Stat(arg)
	if err != nil {
		return nil, fmt.Errorf("checking path: %w", err)
	}
	if !info.IsDir() {
		return []string{arg}, nil
	}
	found, err := findYAMLFiles(arg)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w in %q", errNoYAMLFiles, arg)
	}
	return found, nil
}

func findYAMLFiles(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext == ".yml" || ext == ".yaml" {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("searching %q: %w", root, err)
	}
	return found, nil
}
