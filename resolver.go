package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/google/go-github/v88/github"
	"golang.org/x/sync/errgroup"
)

// resolver resolves targets concurrently and requests each one only once, no
// matter how many files use it. submit and wait must be called from one goroutine.
type resolver struct {
	client   *github.Client
	group    errgroup.Group
	seen     map[target]struct{}
	mu       sync.Mutex
	resolved map[target]resolvedRef
}

func newResolver(client *github.Client, workers int) *resolver {
	r := &resolver{
		client:   client,
		seen:     make(map[target]struct{}),
		resolved: make(map[target]resolvedRef),
	}
	r.group.SetLimit(numberOfWorkers(workers))
	return r
}

func (r *resolver) submit(ctx context.Context, targets []target) {
	for _, t := range targets {
		if _, dup := r.seen[t]; dup {
			continue
		}
		r.seen[t] = struct{}{}
		r.group.Go(func() error {
			r.resolve(ctx, t)
			return nil
		})
	}
}

func (r *resolver) resolve(ctx context.Context, t target) {
	ref, err := resolveTarget(ctx, r.client, t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s: %v\n", t, err)
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
	return r.resolved
}

func numberOfWorkers(workers int) int {
	if workers >= 1 {
		return min(workers, runtime.NumCPU())
	}
	return runtime.NumCPU()
}
