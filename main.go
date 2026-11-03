package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"charm.land/fang/v2"
	"github.com/google/go-github/v88/github"
)

func main() {
	err := fang.Execute(
		context.Background(),
		newRootCmd(),
		fang.WithColorSchemeFunc(fang.AnsiColorScheme),
		fang.WithoutManpage(),
	)
	if err != nil {
		os.Exit(1)
	}
}

// run pins the actions of all files found in args. No file is written until
// every file has been read and every action has been resolved.
func run(ctx context.Context, client *github.Client, cfg config, args []string, stdout io.Writer) error {
	paths, err := collectFiles(args)
	if err != nil {
		return err
	}

	resolver := newResolver(client, cfg.workers)
	workflows := make([]*workflow, 0, len(paths))
	for _, path := range paths {
		wf, err := loadWorkflow(path, cfg)
		if err != nil {
			return err
		}
		workflows = append(workflows, wf)
		resolver.submit(ctx, wf.targets())
	}
	resolved := resolver.wait()

	for _, wf := range workflows {
		changed := wf.pin(resolved)
		if err := wf.write(stdout, changed, cfg.inPlace, len(workflows) > 1); err != nil {
			return fmt.Errorf("writing %q: %w", wf.path, err)
		}
	}
	return nil
}
