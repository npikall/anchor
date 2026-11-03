package main

import (
	"context"
	"runtime"
	"sync"

	"github.com/google/go-github/v93/github"
	"golang.org/x/sync/errgroup"
)

// resolver resolves targets concurrently and requests each one only once, no
// matter how many files use it. submit must be called once, followed by wait.
type resolver struct {
	client   *github.Client
	bar      *progressBar
	group    errgroup.Group
	mu       sync.Mutex
	resolved map[target]resolvedRef
}

func newResolver(client *github.Client, bar *progressBar, workers int) *resolver {
	r := &resolver{
		client:   client,
		bar:      bar,
		resolved: make(map[target]resolvedRef),
	}
	r.group.SetLimit(numberOfWorkers(workers))
	return r
}

func (r *resolver) submit(ctx context.Context, targets []target) {
	unique := uniqueTargets(targets)
	r.bar.start(len(unique))
	for _, t := range unique {
		r.group.Go(func() error {
			r.resolve(ctx, t)
			return nil
		})
	}
}

func (r *resolver) resolve(ctx context.Context, t target) {
	defer r.bar.advance()
	ref, err := resolveTarget(ctx, r.client, t)
	if err != nil {
		r.bar.printf("warning: %s: %v\n", t, err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolved[t] = ref
}

// wait blocks until all submitted targets are done. Targets that could not be
// resolved are missing from the result.
func (r *resolver) wait() map[target]resolvedRef {
	_ = r.group.Wait() // Workers warn about failures and never return an error.
	r.bar.finish()
	return r.resolved
}

func uniqueTargets(targets []target) []target {
	seen := make(map[target]struct{}, len(targets))
	unique := make([]target, 0, len(targets))
	for _, t := range targets {
		if _, dup := seen[t]; !dup {
			seen[t] = struct{}{}
			unique = append(unique, t)
		}
	}
	return unique
}

func numberOfWorkers(workers int) int {
	if workers >= 1 {
		return min(workers, runtime.NumCPU())
	}
	return runtime.NumCPU()
}
