package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/google/go-github/v88/github"
)

const (
	gitSHALength    = 40
	filePermOwnerRW = 0o600
)

var (
	errUsage       = errors.New("usage: anchor [--update] [--in-place] [--verbose] <workflow.yml>")
	errNoTagsFound = errors.New("no tags found")
)

type config struct {
	update  bool
	inPlace bool
	verbose bool
	file    string
}

type job struct {
	lineIdx      int
	owner        string
	repo         string
	ref          string
	repoPath     string
	linePrefix   string
	originalLine string
	update       bool
}

type usesDirective struct {
	prefix   string
	repoPath string
	ref      string
}

func main() {
	cfg, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(1)
	}

	lines, err := readLines(cfg.file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	client := setupGithubClient()
	ctx := context.Background()
	resultMap := resolveAllActions(ctx, client, lines, cfg)

	output := buildOutput(lines, resultMap)
	if err := writeOutput(cfg.file, output, cfg.inPlace); err != nil {
		fmt.Fprintf(os.Stderr, "error writing file: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() (config, error) {
	updateFlag := flag.Bool("u", false, "pin to latest tag instead of current ref")
	inPlaceFlag := flag.Bool("i", false, "overwrite file in place")
	verboseFlag := flag.Bool("v", false, "warn about skipped local/docker actions")
	flag.Parse()

	if flag.NArg() < 1 || flag.Arg(0) == "help" {
		return config{}, errUsage
	}

	return config{
		update:  *updateFlag,
		inPlace: *inPlaceFlag,
		verbose: *verboseFlag,
		file:    flag.Arg(0),
	}, nil
}

func resolveAllActions(ctx context.Context, client *github.Client, lines []string, cfg config) map[int]string {
	jobsCh := make(chan job)
	var wg sync.WaitGroup
	resultMap := dispatchWorkers(ctx, client, &wg, jobsCh)

	go enqueueJobs(lines, jobsCh, cfg)
	wg.Wait()

	return resultMap
}

func enqueueJobs(lines []string, jobsCh chan job, cfg config) {
	defer close(jobsCh)
	for i, line := range lines {
		j, ok := lineToJob(i, line, cfg)
		if ok {
			jobsCh <- j
		}
	}
}

func lineToJob(lineIdx int, line string, cfg config) (job, bool) {
	directive, skip, local := parseUsesLine(line)
	if skip {
		return job{}, false
	}
	if local {
		if cfg.verbose {
			fmt.Fprintf(os.Stderr, "skipping: %s\n", strings.TrimSpace(line))
		}
		return job{}, false
	}
	if isSHA(directive.ref) && !cfg.update {
		return job{}, false
	}
	owner, repo := splitOwnerRepo(directive.repoPath)
	return job{
		lineIdx:      lineIdx,
		owner:        owner,
		repo:         repo,
		ref:          directive.ref,
		repoPath:     directive.repoPath,
		linePrefix:   directive.prefix,
		originalLine: line,
		update:       cfg.update,
	}, true
}

func splitOwnerRepo(repoPath string) (string, string) {
	owner, rest, _ := strings.Cut(repoPath, "/")
	repo, _, _ := strings.Cut(rest, "/")
	return owner, repo
}

func buildOutput(lines []string, resultMap map[int]string) string {
	var sb strings.Builder
	for i, line := range lines {
		if newLine, ok := resultMap[i]; ok {
			sb.WriteString(newLine)
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func writeOutput(filepath, content string, inPlace bool) error {
	if !inPlace {
		fmt.Print(content)
		return nil
	}
	if err := os.WriteFile(filepath, []byte(content), filePermOwnerRW); err != nil {
		return fmt.Errorf("writing %q: %w", filepath, err)
	}
	return nil
}

func dispatchWorkers(ctx context.Context, client *github.Client, wg *sync.WaitGroup, jobsCh chan job) map[int]string {
	resultMap := make(map[int]string)
	var mu sync.Mutex

	for range runtime.NumCPU() {
		wg.Go(func() {
			for j := range jobsCh {
				newLine, jobErr := processJob(ctx, client, j)
				if jobErr != nil {
					fmt.Fprintf(os.Stderr, "warning: %s/%s@%s: %v\n", j.owner, j.repo, j.ref, jobErr)
					newLine = j.originalLine
				}
				mu.Lock()
				resultMap[j.lineIdx] = newLine
				mu.Unlock()
			}
		})
	}
	return resultMap
}

func setupGithubClient() *github.Client {
	opts := []github.ClientOptionsFunc{}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		opts = append(opts, github.WithAuthToken(token))
	}
	client, err := github.NewClient(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return client
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", path, err)
	}
	lines, scanErr := scanLines(f)
	if closeErr := f.Close(); scanErr == nil && closeErr != nil {
		return lines, fmt.Errorf("closing %q: %w", path, closeErr)
	}
	return lines, scanErr
}

func scanLines(r io.Reader) ([]string, error) {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return lines, fmt.Errorf("scanning lines: %w", err)
	}
	return lines, nil
}

func parseUsesLine(line string) (usesDirective, bool, bool) {
	prefix, value, ok := extractUsesValue(line)
	if !ok {
		return usesDirective{}, true, false
	}

	if strings.HasPrefix(value, "./") || strings.HasPrefix(value, "docker://") {
		return usesDirective{}, false, true
	}

	atIdx := strings.LastIndex(value, "@")
	if atIdx == -1 {
		return usesDirective{}, true, false
	}

	return usesDirective{
		prefix:   prefix,
		repoPath: value[:atIdx],
		ref:      value[atIdx+1:],
	}, false, false
}

func extractUsesValue(line string) (string, string, bool) {
	usesKeyEnd := findUsesKeyEnd(line)
	if usesKeyEnd == -1 {
		return "", "", false
	}

	valueStart := usesKeyEnd
	for valueStart < len(line) && line[valueStart] == ' ' {
		valueStart++
	}
	prefix := line[:valueStart]

	raw := line[valueStart:]
	if ci := strings.Index(raw, " #"); ci != -1 {
		raw = strings.TrimSpace(raw[:ci])
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", "", false
	}
	return prefix, value, true
}

func findUsesKeyEnd(line string) int {
	if idx := strings.Index(line, "- uses:"); idx != -1 && !strings.Contains(line[:idx], "#") {
		return idx + len("- uses:")
	}
	if idx := strings.Index(line, "uses:"); idx != -1 && !strings.Contains(line[:idx], "#") {
		return idx + len("uses:")
	}
	return -1
}

func isSHA(ref string) bool {
	return len(ref) == gitSHALength
}

func processJob(ctx context.Context, client *github.Client, j job) (string, error) {
	sha, version, err := resolveActionRef(ctx, client, j)
	if err != nil {
		return j.originalLine, err
	}
	return j.linePrefix + j.repoPath + "@" + sha + " # " + version, nil
}

func resolveActionRef(ctx context.Context, client *github.Client, j job) (string, string, error) {
	if j.update || isSHA(j.ref) {
		return latestTag(ctx, client, j.owner, j.repo)
	}
	return resolveRef(ctx, client, j.owner, j.repo, j.ref)
}

func resolveRef(ctx context.Context, client *github.Client, owner, repo, ref string) (string, string, error) {
	gitRef, _, err := client.Git.GetRef(ctx, owner, repo, "tags/"+ref)
	if err == nil {
		sha, tagErr := resolveTagSHA(ctx, client, owner, repo, gitRef)
		return sha, ref, tagErr
	}

	gitRef, _, err = client.Git.GetRef(ctx, owner, repo, "heads/"+ref)
	if err != nil {
		return "", "", fmt.Errorf("could not resolve ref %q: %w", ref, err)
	}
	return gitRef.Object.GetSHA(), ref, nil
}

func resolveTagSHA(ctx context.Context, client *github.Client, owner, repo string, gitRef *github.Reference) (string, error) {
	if gitRef.Object.GetType() != "tag" {
		return gitRef.Object.GetSHA(), nil
	}
	tag, _, err := client.Git.GetTag(ctx, owner, repo, gitRef.Object.GetSHA())
	if err != nil {
		return "", fmt.Errorf("fetching annotated tag: %w", err)
	}
	return tag.Object.GetSHA(), nil
}

func latestTag(ctx context.Context, client *github.Client, owner, repo string) (string, string, error) {
	tags, _, err := client.Repositories.ListTags(ctx, owner, repo, nil)
	if err != nil {
		return "", "", fmt.Errorf("listing tags for %s/%s: %w", owner, repo, err)
	}
	if len(tags) == 0 {
		return "", "", fmt.Errorf("%s/%s: %w", owner, repo, errNoTagsFound)
	}
	return tags[0].Commit.GetSHA(), tags[0].GetName(), nil
}
