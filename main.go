package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"time"

	"charm.land/fang/v2"
	"github.com/google/go-github/v88/github"
)

// variables set during comptime
var (
	commit  string
	version string
)

func main() {
	err := fang.Execute(
		context.Background(),
		newRootCmd(),
		fang.WithColorSchemeFunc(fang.AnsiColorScheme),
		fang.WithCommit(commit),
		fang.WithoutManpage(),
		fang.WithVersion(version),
	)
	if err != nil {
		os.Exit(1)
	}
}

// run pins the actions of all files found in args. No file is written until
// every file has been read and every action has been resolved.
func run(ctx context.Context, client *github.Client, store *cache, cfg config, args []string, stdout, stderr io.Writer) error {
	paths, err := collectFiles(args)
	if err != nil {
		return err
	}

	workflows := make([]*workflow, 0, len(paths))
	var targets []target
	for _, path := range paths {
		wf, err := loadWorkflow(path, cfg, stderr)
		if err != nil {
			return err
		}
		workflows = append(workflows, wf)
		targets = append(targets, wf.targets()...)
	}

	resolved := resolveTargets(ctx, client, store, cfg, targets, stderr)

	for _, wf := range workflows {
		changed := wf.pin(resolved)
		if err := wf.write(stdout, changed, cfg.inPlace, len(workflows) > 1); err != nil {
			return err
		}
	}
	return nil
}

// resolveTargets asks GitHub only for targets the cache cannot answer. Failing
// to read or write the cache costs requests, but never the result.
func resolveTargets(ctx context.Context, client *github.Client, store *cache, cfg config, targets []target, stderr io.Writer) map[target]resolvedRef {
	resolved := reusableEntries(ctx, store, cfg, stderr)
	missing := slices.DeleteFunc(targets, func(t target) bool {
		_, ok := resolved[t]
		return ok
	})

	bar := newProgressBar(stderr, isTerminal(stderr))
	resolver := newResolver(client, bar, cfg.workers)
	resolver.submit(ctx, missing)
	fresh := resolver.wait()

	if err := store.save(ctx, fresh, time.Now()); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	maps.Copy(resolved, fresh)
	return resolved
}

func reusableEntries(ctx context.Context, store *cache, cfg config, stderr io.Writer) map[target]resolvedRef {
	if cfg.refresh {
		return make(map[target]resolvedRef)
	}
	entries, err := store.load(ctx, time.Now())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: %v\n", err)
		return make(map[target]resolvedRef)
	}
	return entries
}
