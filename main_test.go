package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v88/github"
)

var (
	checkoutSHA = strings.Repeat("a", gitSHALength)
	latestSHA   = strings.Repeat("b", gitSHALength)
)

// fakeGithub serves one tag and a latest tag of actions/checkout and counts requests.
func fakeGithub(t *testing.T) (*github.Client, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/actions/checkout/git/ref/tags/v4", func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprintf(w, `{"object":{"type":"commit","sha":%q}}`, checkoutSHA)
	})
	mux.HandleFunc("/repos/actions/checkout/tags", func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprintf(w, `[{"name":"v5","commit":{"sha":%q}}]`, latestSHA)
	})
	return newTestClient(t, mux), &requests
}

func newTestClient(t *testing.T, handler http.Handler) *github.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	baseURL := server.URL + "/"
	client, err := github.NewClient(github.WithURLs(&baseURL, &baseURL))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s:\n got %q\nwant %q", path, got, want)
	}
}

func TestRunPinsDirectoryInPlaceWithSharedRequest(t *testing.T) {
	client, requests := fakeGithub(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "ci.yml")
	second := filepath.Join(dir, "nested", "release.yaml")
	noActions := filepath.Join(dir, "dependabot.yml")
	writeFile(t, first, "- uses: actions/checkout@v4\n")
	writeFile(t, second, "  uses: actions/checkout@v4\n")
	writeFile(t, noActions, "version: 2") // No trailing newline: must stay untouched.

	err := run(t.Context(), client, nil, config{inPlace: true, workers: 2}, []string{dir}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	assertFile(t, first, "- uses: actions/checkout@"+checkoutSHA+" # v4\n")
	assertFile(t, second, "  uses: actions/checkout@"+checkoutSHA+" # v4\n")
	assertFile(t, noActions, "version: 2")
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestRunUpdateRequestsLatestTagOncePerRepo(t *testing.T) {
	client, requests := fakeGithub(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "a.yml")
	second := filepath.Join(dir, "b.yml")
	writeFile(t, first, "- uses: actions/checkout@v3\n")
	writeFile(t, second, "- uses: actions/checkout@"+checkoutSHA+" # v4\n")

	err := run(t.Context(), client, nil, config{update: true, inPlace: true}, []string{first, second}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	want := "- uses: actions/checkout@" + latestSHA + " # v5\n"
	assertFile(t, first, want)
	assertFile(t, second, want)
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestRunUpdatePicksHighestSemverTagAcrossPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/taiki-e/install-action/tags", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprintf(w, `[{"name":"v2.9.0","commit":{"sha":%q}},{"name":"v2.90.0","commit":{"sha":%q}},{"name":"v2","commit":{"sha":%q}}]`,
				checkoutSHA, latestSHA, checkoutSHA)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?page=2>; rel="next"`, r.Host, r.URL.Path))
		fmt.Fprintf(w, `[{"name":"zola","commit":{"sha":%q}},{"name":"v3.0.0-rc.1","commit":{"sha":%q}}]`, checkoutSHA, checkoutSHA)
	})
	path := filepath.Join(t.TempDir(), "ci.yml")
	writeFile(t, path, "- uses: taiki-e/install-action@v2\n")

	err := run(t.Context(), newTestClient(t, mux), nil, config{update: true, inPlace: true}, []string{path}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	assertFile(t, path, "- uses: taiki-e/install-action@"+latestSHA+" # v2.90.0\n")
}

func TestRunUpdateLeavesActionWithoutSemverTagsUnchanged(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/named/tags", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `[{"name":"nightly","commit":{"sha":%q}}]`, latestSHA)
	})
	path := filepath.Join(t.TempDir(), "ci.yml")
	original := "- uses: acme/named@main\n"
	writeFile(t, path, original)

	err := run(t.Context(), newTestClient(t, mux), nil, config{update: true, inPlace: true}, []string{path}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	assertFile(t, path, original)
}

func TestIsNewerTag(t *testing.T) {
	tests := []struct {
		name, current string
		want          bool
	}{
		{"v2.90.0", "", true},
		{"v2.90.0", "v2.9.0", true},
		{"v2.9.0", "v2.90.0", false},
		{"v2.0.0", "v2", true},
		{"v2", "v2.0.0", false},
		{"v3", "v2.90.0", true},
		{"1.2.3", "v1.2.2", true},
		{"zola", "", false},
		{"v3.0.0-rc.1", "v2.90.0", false},
	}
	for _, tt := range tests {
		if got := isNewerTag(tt.name, tt.current); got != tt.want {
			t.Errorf("isNewerTag(%q, %q) = %v, want %v", tt.name, tt.current, got, tt.want)
		}
	}
}

func TestRunPrintsWithoutModifyingFiles(t *testing.T) {
	client, _ := fakeGithub(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "a.yml")
	second := filepath.Join(dir, "b.yml")
	original := "- uses: actions/checkout@v4\n"
	writeFile(t, first, original)
	writeFile(t, second, original)
	pinned := "- uses: actions/checkout@" + checkoutSHA + " # v4\n"

	var multiple bytes.Buffer
	if err := run(t.Context(), client, nil, config{}, []string{first, second}, &multiple); err != nil {
		t.Fatal(err)
	}
	want := "# " + first + "\n" + pinned + "# " + second + "\n" + pinned
	if multiple.String() != want {
		t.Errorf("got %q, want %q", multiple.String(), want)
	}
	assertFile(t, first, original)

	var single bytes.Buffer
	if err := run(t.Context(), client, nil, config{}, []string{first}, &single); err != nil {
		t.Fatal(err)
	}
	if single.String() != pinned {
		t.Errorf("got %q, want %q", single.String(), pinned)
	}
}

func TestRunWritesNothingWhenAFileCannotBeRead(t *testing.T) {
	client, _ := fakeGithub(t)
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.yml")
	original := "- uses: actions/checkout@v4\n"
	writeFile(t, valid, original)
	badLine := strings.Repeat("x", 70*1024) + "\n" // Longer than the scanner limit.
	bad := filepath.Join(dir, "z.yml")
	writeFile(t, bad, badLine)

	if err := run(t.Context(), client, nil, config{inPlace: true}, []string{dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error")
	}
	assertFile(t, valid, original)
}

func TestRunHandlesQuotedValues(t *testing.T) {
	pinned := "actions/checkout@" + checkoutSHA
	tests := []struct{ name, input, want string }{
		{"double quotes", `- uses: "actions/checkout@v4"`, `- uses: "` + pinned + `" # v4`},
		{"single quotes", `- uses: 'actions/checkout@v4'`, `- uses: '` + pinned + `' # v4`},
		{"quotes and comment", `- uses: "actions/checkout@v4" # old`, `- uses: "` + pinned + `" # v4`},
		{"quoted SHA stays", `- uses: "` + pinned + `"`, `- uses: "` + pinned + `"`},
		{"quoted local stays", `- uses: "./local"`, `- uses: "./local"`},
		{"quoted docker stays", `- uses: 'docker://alpine:3'`, `- uses: 'docker://alpine:3'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := fakeGithub(t)
			path := filepath.Join(t.TempDir(), "ci.yml")
			writeFile(t, path, tt.input+"\n")

			var out bytes.Buffer
			if err := run(t.Context(), client, nil, config{}, []string{path}, &out); err != nil {
				t.Fatal(err)
			}
			if want := tt.want + "\n"; out.String() != want {
				t.Errorf("got %q, want %q", out.String(), want)
			}
		})
	}
}

func TestCollectFiles(t *testing.T) {
	dir := t.TempDir()
	workflow := filepath.Join(dir, "workflows", "ci.yml")
	action := filepath.Join(dir, "actions", "x", "action.yaml")
	explicit := filepath.Join(dir, "explicit.txt")
	writeFile(t, workflow, "")
	writeFile(t, action, "")
	writeFile(t, explicit, "")
	writeFile(t, filepath.Join(dir, "notes.md"), "")
	writeFile(t, filepath.Join(dir, ".git", "config.yml"), "")

	got, err := collectFiles([]string{workflow, dir, explicit})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{workflow, action, explicit}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCollectFilesErrors(t *testing.T) {
	empty := t.TempDir()
	missing := filepath.Join(empty, "missing.yml")
	for _, arg := range []string{empty, missing} {
		if _, err := collectFiles([]string{arg}); err == nil {
			t.Errorf("collectFiles(%q): expected error", arg)
		}
	}
}

func TestProgressBarDrawsAndClears(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	bar := newProgressBar(&out, true)

	bar.start(2)
	bar.advance()
	if !strings.HasSuffix(out.String(), " 1/2") {
		t.Errorf("after advance: got %q", out.String())
	}
	bar.printf("warning: %s\n", "oops")
	if !strings.Contains(out.String(), clearLine+"warning: oops\n\r") {
		t.Errorf("warning must replace the bar and the bar must follow: got %q", out.String())
	}
	bar.finish()
	if !strings.HasSuffix(out.String(), clearLine) {
		t.Errorf("after finish: got %q", out.String())
	}
}

func TestProgressBarDisabledOnlyPrintsMessages(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	bar := newProgressBar(&out, false)

	bar.start(1)
	bar.printf("warning: %s\n", "oops")
	bar.advance()
	bar.finish()

	if got, want := out.String(), "warning: oops\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProgressBarWithoutItemsDrawsNothing(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	bar := newProgressBar(&out, true)

	bar.start(0)
	bar.finish()

	if out.String() != "" {
		t.Errorf("got %q, want no output", out.String())
	}
}

// runOnFile runs anchor on a new file with the given content and returns what it printed.
func runOnFile(t *testing.T, client *github.Client, store *cache, cfg config, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ci.yml")
	writeFile(t, path, content)
	var out bytes.Buffer
	if err := run(t.Context(), client, store, cfg, []string{path}, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestRunReusesCachedActionsInLaterRuns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cfg   config
		input string
		want  string
	}{
		{"ref", config{}, "- uses: actions/checkout@v4\n", "- uses: actions/checkout@" + checkoutSHA + " # v4\n"},
		{"latest tag", config{update: true}, "- uses: actions/checkout@v4\n", "- uses: actions/checkout@" + latestSHA + " # v5\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, requests := fakeGithub(t)
			store := openTestCache(t)

			for range 2 {
				if got := runOnFile(t, client, store, tt.cfg, tt.input); got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

func TestRunRequestsOnlyActionsMissingFromCache(t *testing.T) {
	t.Parallel()
	client, requests := fakeGithub(t)
	store := openTestCache(t)
	cached := map[target]resolvedRef{{owner: "acme", repo: "cached", ref: "v1"}: {sha: latestSHA, version: "v1"}}
	if err := store.save(t.Context(), cached, time.Now()); err != nil {
		t.Fatal(err)
	}

	got := runOnFile(t, client, store, config{}, "- uses: acme/cached@v1\n- uses: actions/checkout@v4\n")

	want := "- uses: acme/cached@" + latestSHA + " # v1\n- uses: actions/checkout@" + checkoutSHA + " # v4\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestRunRequestsExpiredActionAgain(t *testing.T) {
	t.Parallel()
	client, requests := fakeGithub(t)
	store := openTestCache(t)
	action := target{owner: "actions", repo: "checkout", ref: "v4"}
	expired := time.Now().Add(-movingRefTTL)
	if err := store.save(t.Context(), map[target]resolvedRef{action: {sha: latestSHA, version: "v4"}}, expired); err != nil {
		t.Fatal(err)
	}
	want := "- uses: actions/checkout@" + checkoutSHA + " # v4\n"

	for range 2 { // The second run must find the renewed entry.
		if got := runOnFile(t, client, store, config{}, "- uses: actions/checkout@v4\n"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestRunRefreshIgnoresCacheAndReplacesEntry(t *testing.T) {
	t.Parallel()
	client, requests := fakeGithub(t)
	store := openTestCache(t)
	action := target{owner: "actions", repo: "checkout", ref: "v4"}
	if err := store.save(t.Context(), map[target]resolvedRef{action: {sha: latestSHA, version: "v4"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	want := "- uses: actions/checkout@" + checkoutSHA + " # v4\n"

	for _, cfg := range []config{{refresh: true}, {}} {
		if got := runOnFile(t, client, store, cfg, "- uses: actions/checkout@v4\n"); got != want {
			t.Errorf("refresh=%v: got %q, want %q", cfg.refresh, got, want)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestRunDoesNotCacheFailures(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	store := openTestCache(t)
	input := "- uses: acme/missing@v1\n"

	if got := runOnFile(t, client, store, config{}, input); got != input {
		t.Errorf("got %q, want %q", got, input)
	}
	afterFirstRun := requests.Load()
	runOnFile(t, client, store, config{}, input)

	if got := requests.Load(); got != 2*afterFirstRun {
		t.Errorf("requests = %d, want %d", got, 2*afterFirstRun)
	}
}

func TestRunWarnsAndStillPinsWhenCacheFails(t *testing.T) {
	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	realStderr := os.Stderr
	os.Stderr = stderr
	t.Cleanup(func() { os.Stderr = realStderr })

	client, _ := fakeGithub(t)
	store := openTestCache(t)
	store.close() // Every query fails from now on.

	got := runOnFile(t, client, store, config{}, "- uses: actions/checkout@v4\n")

	if want := "- uses: actions/checkout@" + checkoutSHA + " # v4\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	warnings, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"warning: reading cache: ", "warning: writing cache: "} {
		if !strings.Contains(string(warnings), want) {
			t.Errorf("stderr %q does not contain %q", warnings, want)
		}
	}
}
