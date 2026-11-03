package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/mod/semver"
	_ "modernc.org/sqlite" // Pure Go driver, so go install keeps working without cgo.
)

const (
	// movingRefTTL is how long a resolved ref that can point to another commit
	// later, such as v4, main or the latest tag, may be reused.
	movingRefTTL = 24 * time.Hour

	cacheDirPerm = 0o700

	// The busy timeout makes a second anchor process wait for the lock instead
	// of failing.
	cacheDSNOptions = "?_pragma=busy_timeout(5000)"

	createCacheTable = `CREATE TABLE IF NOT EXISTS actions (
		owner       TEXT    NOT NULL,
		repo        TEXT    NOT NULL,
		ref         TEXT    NOT NULL,
		sha         TEXT    NOT NULL,
		version     TEXT    NOT NULL,
		resolved_at INTEGER NOT NULL,
		PRIMARY KEY (owner, repo, ref)
	)`
	selectCacheEntries = `SELECT owner, repo, ref, sha, version, resolved_at FROM actions`
	upsertCacheEntry   = `INSERT OR REPLACE INTO actions
		(owner, repo, ref, sha, version, resolved_at) VALUES (?, ?, ?, ?, ?, ?)`
)

// cache remembers resolved targets across runs. It is only an optimization, so
// a nil cache is valid and stores nothing. It is not used by the resolver's
// workers: entries are loaded before they start and saved after they finished.
type cache struct {
	db *sql.DB
}

// openDefaultCache opens the cache of the current user. Without a usable cache
// anchor still works, so failing to open it is only a warning.
func openDefaultCache(ctx context.Context, stderr io.Writer) *cache {
	path, err := defaultCachePath(runtime.GOOS)
	var store *cache
	if err == nil {
		store, err = openCache(ctx, path)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: cache disabled: %v\n", err)
	}
	return store
}

func defaultCachePath(goos string) (string, error) {
	dir, err := userDataDir(goos)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "anchor", "anchor.db"), nil
}

// userDataDir returns $XDG_DATA_HOME if set, or else the platform's convention.
func userDataDir(goos string) (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}
	if goos == "darwin" || goos == "windows" {
		dir, err := os.UserConfigDir() // Data and config share a directory here.
		if err != nil {
			return "", fmt.Errorf("finding data directory: %w", err)
		}
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding data directory: %w", err)
	}
	return filepath.Join(home, ".local", "share"), nil
}

func openCache(ctx context.Context, path string) (*cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), cacheDirPerm); err != nil {
		return nil, fmt.Errorf("creating cache directory: %w", err)
	}
	db, err := sql.Open("sqlite", path+cacheDSNOptions)
	if err != nil {
		return nil, fmt.Errorf("opening cache %q: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, createCacheTable); err != nil {
		return nil, errors.Join(fmt.Errorf("preparing cache %q: %w", path, err), db.Close())
	}
	return &cache{db: db}, nil
}

func (c *cache) close() {
	if c != nil {
		_ = c.db.Close() // Everything was committed by save.
	}
}

// load returns all entries that may still be reused at the given time.
func (c *cache) load(ctx context.Context, now time.Time) (map[target]resolvedRef, error) {
	if c == nil {
		return map[target]resolvedRef{}, nil
	}
	rows, err := c.db.QueryContext(ctx, selectCacheEntries)
	if err != nil {
		return nil, fmt.Errorf("reading cache: %w", err)
	}
	defer func() { _ = rows.Close() }() // Read errors are reported by rows.Err.

	entries := make(map[target]resolvedRef)
	for rows.Next() {
		var t target
		var ref resolvedRef
		var resolvedAt int64
		if err := rows.Scan(&t.owner, &t.repo, &t.ref, &ref.sha, &ref.version, &resolvedAt); err != nil {
			return nil, fmt.Errorf("reading cache: %w", err)
		}
		if isReusable(t.ref, now.Sub(time.Unix(resolvedAt, 0))) {
			entries[t] = ref
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading cache: %w", err)
	}
	return entries, nil
}

// save stores all entries or none of them. Saving a target again replaces it.
func (c *cache) save(ctx context.Context, entries map[target]resolvedRef, resolvedAt time.Time) error {
	if c == nil {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("writing cache: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // No effect after a commit.

	for t, ref := range entries {
		_, err := tx.ExecContext(ctx, upsertCacheEntry, t.owner, t.repo, t.ref, ref.sha, ref.version, resolvedAt.Unix())
		if err != nil {
			return fmt.Errorf("writing cache: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("writing cache: %w", err)
	}
	return nil
}

// isReusable decides whether an entry of the given ref and age can be used
// instead of asking GitHub.
func isReusable(ref string, age time.Duration) bool {
	return isExactVersion(ref) || age < movingRefTTL
}

// isExactVersion reports whether ref names one full version, like v1.2.3. By
// convention these tags keep pointing to the same commit.
func isExactVersion(ref string) bool {
	version := withVPrefix(ref)
	return semver.IsValid(version) && semver.Canonical(version) == version
}
