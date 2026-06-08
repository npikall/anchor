package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/google/go-github/v88/github"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

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

func main() {
	updateFlag := flag.Bool("update", false, "pin to latest tag instead of current ref")
	inPlaceFlag := flag.Bool("in-place", false, "overwrite file in place")
	verboseFlag := flag.Bool("verbose", false, "warn about skipped local/docker actions")
	flag.Parse()

	if flag.NArg() < 1 || flag.Arg(0) == "help" {
		fmt.Fprintln(os.Stderr, "usage: anchor [--update] [--in-place] [--verbose] <workflow.yml>")
		os.Exit(1)
	}

	filepath := flag.Arg(0)
	lines, err := readLines(filepath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	client := setupGithubClient()
	ctx := context.Background()
	jobsCh := make(chan job)
	var wg sync.WaitGroup
	resultMap := dispatchWorker(ctx, client, &wg, jobsCh)

	go func() {
		for i, line := range lines {
			prefix, repoPath, ref, skip, local := parseUsesLine(line)
			if skip {
				continue
			}
			if local {
				if *verboseFlag {
					fmt.Fprintf(os.Stderr, "skipping: %s\n", strings.TrimSpace(line))
				}
				continue
			}
			if isSHA(ref) && !*updateFlag {
				continue
			}
			owner, rest, _ := strings.Cut(repoPath, "/")
			repo, _, _ := strings.Cut(rest, "/")
			jobsCh <- job{
				lineIdx:      i,
				owner:        owner,
				repo:         repo,
				ref:          ref,
				repoPath:     repoPath,
				linePrefix:   prefix,
				originalLine: line,
				update:       *updateFlag,
			}
		}
		close(jobsCh)
	}()

	wg.Wait()

	var sb strings.Builder
	for i, line := range lines {
		if newLine, ok := resultMap[i]; ok {
			sb.WriteString(newLine)
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if *inPlaceFlag {
		if err := os.WriteFile(filepath, []byte(sb.String()), 0o600); err != nil { //nolint:gosec
			fmt.Fprintf(os.Stderr, "error writing file: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Print(sb.String())
	}
}

func dispatchWorker(ctx context.Context, client *github.Client, wg *sync.WaitGroup, jobsCh chan job) map[int]string {
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
		return nil, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// parseUsesLine extracts prefix, repoPath, and ref from a uses: line.
// skip=true if not a uses: line. local=true if local/docker action (not pinnable).
func parseUsesLine(line string) (prefix, repoPath, ref string, skip, local bool) {
	usesKeyEnd := -1
	if idx := strings.Index(line, "- uses:"); idx != -1 && !strings.Contains(line[:idx], "#") {
		usesKeyEnd = idx + len("- uses:")
	} else if idx := strings.Index(line, "uses:"); idx != -1 && !strings.Contains(line[:idx], "#") {
		usesKeyEnd = idx + len("uses:")
	}
	if usesKeyEnd == -1 {
		return "", "", "", true, false
	}

	valueStart := usesKeyEnd
	for valueStart < len(line) && line[valueStart] == ' ' {
		valueStart++
	}
	prefix = line[:valueStart]

	value := line[valueStart:]
	if ci := strings.Index(value, " #"); ci != -1 {
		value = strings.TrimSpace(value[:ci])
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", "", true, false
	}

	if strings.HasPrefix(value, "./") || strings.HasPrefix(value, "docker://") {
		return "", "", "", false, true
	}

	atIdx := strings.LastIndex(value, "@")
	if atIdx == -1 {
		return "", "", "", true, false
	}

	return prefix, value[:atIdx], value[atIdx+1:], false, false
}

func isSHA(ref string) bool {
	return shaRe.MatchString(ref)
}

func processJob(ctx context.Context, client *github.Client, j job) (string, error) {
	var sha, version string
	var err error

	if j.update || isSHA(j.ref) {
		sha, version, err = latestTag(ctx, client, j.owner, j.repo)
	} else {
		sha, version, err = resolveRef(ctx, client, j.owner, j.repo, j.ref)
	}
	if err != nil {
		return j.originalLine, err
	}

	return j.linePrefix + j.repoPath + "@" + sha + " # " + version, nil
}

func resolveRef(ctx context.Context, client *github.Client, owner, repo, ref string) (sha, version string, err error) {
	gitRef, _, err := client.Git.GetRef(ctx, owner, repo, "tags/"+ref)
	if err == nil {
		if gitRef.Object.GetType() == "tag" {
			tag, _, tagErr := client.Git.GetTag(ctx, owner, repo, gitRef.Object.GetSHA())
			if tagErr != nil {
				return "", "", tagErr
			}
			return tag.Object.GetSHA(), ref, nil
		}
		return gitRef.Object.GetSHA(), ref, nil
	}

	gitRef, _, err = client.Git.GetRef(ctx, owner, repo, "heads/"+ref)
	if err != nil {
		return "", "", fmt.Errorf("could not resolve ref %q: %w", ref, err)
	}
	return gitRef.Object.GetSHA(), ref, nil
}

func latestTag(ctx context.Context, client *github.Client, owner, repo string) (sha, version string, err error) {
	tags, _, err := client.Repositories.ListTags(ctx, owner, repo, nil)
	if err != nil {
		return "", "", err
	}
	if len(tags) == 0 {
		return "", "", fmt.Errorf("no tags found for %s/%s", owner, repo)
	}
	return tags[0].Commit.GetSHA(), tags[0].GetName(), nil
}
