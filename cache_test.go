package main

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestCache(t *testing.T) *cache {
	t.Helper()
	store, err := openCache(t.Context(), filepath.Join(t.TempDir(), "dir with spaces", "anchor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.close)
	return store
}

func TestCacheLoadsWhatWasSavedAfterReopening(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "anchor.db")
	entries := map[target]resolvedRef{
		{owner: "actions", repo: "checkout", ref: "v4.2.2"}: {sha: checkoutSHA, version: "v4.2.2"},
		{owner: "actions", repo: "checkout", ref: "v4"}:     {sha: latestSHA, version: "v4"},
		{owner: "actions", repo: "checkout"}:                {sha: latestSHA, version: "v5.0.0"},
	}
	now := time.Now()

	first, err := openCache(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.save(t.Context(), entries, now); err != nil {
		t.Fatal(err)
	}
	first.close()

	second, err := openCache(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	got, err := second.load(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got, entries) {
		t.Errorf("got %v, want %v", got, entries)
	}
}

func TestCacheSaveReplacesEntryOfSameTarget(t *testing.T) {
	t.Parallel()
	store := openTestCache(t)
	action := target{owner: "actions", repo: "checkout", ref: "v4"}
	now := time.Now()

	for _, sha := range []string{checkoutSHA, latestSHA, latestSHA} {
		if err := store.save(t.Context(), map[target]resolvedRef{action: {sha: sha, version: "v4"}}, now); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.load(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[target]resolvedRef{action: {sha: latestSHA, version: "v4"}}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCacheLoadExpiresMovingRefsButNotExactVersions(t *testing.T) {
	t.Parallel()
	exact := target{owner: "a", repo: "exact", ref: "v1.2.3"}
	major := target{owner: "a", repo: "major", ref: "v1"}
	branch := target{owner: "a", repo: "branch", ref: "main"}
	latest := target{owner: "a", repo: "latest"}
	entries := map[target]resolvedRef{
		exact:  {sha: checkoutSHA, version: "v1.2.3"},
		major:  {sha: checkoutSHA, version: "v1"},
		branch: {sha: checkoutSHA, version: "main"},
		latest: {sha: checkoutSHA, version: "v1.2.3"},
	}
	savedAt := time.Unix(time.Now().Unix(), 0) // Whole seconds, like the stored time.

	tests := []struct {
		name string
		age  time.Duration
		want []target
	}{
		{"just saved", 0, []target{exact, major, branch, latest}},
		{"just before expiry", movingRefTTL - time.Second, []target{exact, major, branch, latest}},
		{"at expiry", movingRefTTL, []target{exact}},
		{"years later", 10 * 365 * 24 * time.Hour, []target{exact}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := openTestCache(t)
			if err := store.save(t.Context(), entries, savedAt); err != nil {
				t.Fatal(err)
			}
			got, err := store.load(t.Context(), savedAt.Add(tt.age))
			if err != nil {
				t.Fatal(err)
			}
			want := make(map[target]resolvedRef)
			for _, action := range tt.want {
				want[action] = entries[action]
			}
			if !maps.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

func TestIsExactVersion(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"v1.2.3":      true,
		"1.2.3":       true,
		"v1.2.3-rc.1": true,
		"v1":          false,
		"v1.2":        false,
		"main":        false,
		latestRef:     false,
	}
	for ref, want := range tests {
		if got := isExactVersion(ref); got != want {
			t.Errorf("isExactVersion(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestOpenCacheFailsOnFileThatIsNotADatabase(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "anchor.db")
	writeFile(t, path, "this is not a sqlite database, it only has enough bytes to be read as one")

	if store, err := openCache(t.Context(), path); err == nil {
		store.close()
		t.Fatal("expected error")
	}
}

func TestNilCacheStoresNothing(t *testing.T) {
	t.Parallel()
	var store *cache
	now := time.Now()

	if err := store.save(t.Context(), map[target]resolvedRef{{owner: "a", repo: "b"}: {sha: checkoutSHA}}, now); err != nil {
		t.Fatal(err)
	}
	got, err := store.load(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
	store.close()
}

func TestDefaultCachePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	xdg := t.TempDir()

	tests := []struct {
		name, goos, xdg, want string
	}{
		{"linux", "linux", "", filepath.Join(home, ".local", "share", "anchor", "anchor.db")},
		{"macOS", "darwin", "", filepath.Join(configDir, "anchor", "anchor.db")},
		{"windows", "windows", "", filepath.Join(configDir, "anchor", "anchor.db")},
		{"XDG wins", "darwin", xdg, filepath.Join(xdg, "anchor", "anchor.db")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", tt.xdg)
			got, err := defaultCachePath(tt.goos)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultCachePathFailsWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	for _, goos := range []string{"linux", "darwin"} {
		if got, err := defaultCachePath(goos); err == nil {
			t.Errorf("%s: got %q, want error", goos, got)
		}
	}
}

func TestOpenDefaultCacheWarnsAndContinuesWithoutCache(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	writeFile(t, notADir, "")
	t.Setenv("XDG_DATA_HOME", notADir)

	var stderr strings.Builder
	if store := openDefaultCache(t.Context(), &stderr); store != nil {
		t.Errorf("got %v, want no cache", store)
	}
	if !strings.HasPrefix(stderr.String(), "warning: cache disabled: ") {
		t.Errorf("got %q, want a warning", stderr.String())
	}
}
