package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v88/github"
	"golang.org/x/mod/semver"
)

const (
	// latestRef is the ref of a target that should resolve to the latest tag.
	latestRef = ""

	tagsPerPage = 100
)

var errNoSemverTags = errors.New("no semver tags found")

// target is what gets requested from GitHub. Subpaths of a repository
// (owner/repo/path@ref) share the same target.
type target struct {
	owner string
	repo  string
	ref   string
}

func (t target) String() string {
	if t.ref == latestRef {
		return t.owner + "/" + t.repo
	}
	return t.owner + "/" + t.repo + "@" + t.ref
}

type resolvedRef struct {
	sha     string
	version string
}

func setupGithubClient(opts ...github.ClientOptionsFunc) (*github.Client, error) {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		opts = append(opts, github.WithAuthToken(token))
	}
	client, err := github.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating github client: %w", err)
	}
	return client, nil
}

func resolveTarget(ctx context.Context, client *github.Client, t target) (resolvedRef, error) {
	if t.ref == latestRef {
		return latestTag(ctx, client, t.owner, t.repo)
	}
	return resolveRef(ctx, client, t.owner, t.repo, t.ref)
}

func resolveRef(ctx context.Context, client *github.Client, owner, repo, ref string) (resolvedRef, error) {
	gitRef, _, err := client.Git.GetRef(ctx, owner, repo, "tags/"+ref)
	if err == nil {
		sha, tagErr := resolveTagSHA(ctx, client, owner, repo, gitRef)
		return resolvedRef{sha: sha, version: ref}, tagErr
	}

	gitRef, _, err = client.Git.GetRef(ctx, owner, repo, "heads/"+ref)
	if err != nil {
		return resolvedRef{}, fmt.Errorf("could not resolve ref %q: %w", ref, err)
	}
	return resolvedRef{sha: gitRef.Object.GetSHA(), version: ref}, nil
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

// latestTag returns the highest stable semver tag. It reads every page because
// GitHub does not sort tags by version, and named tags can fill the first pages.
func latestTag(ctx context.Context, client *github.Client, owner, repo string) (resolvedRef, error) {
	var latest *github.RepositoryTag
	opts := &github.ListOptions{PerPage: tagsPerPage}
	for {
		tags, resp, err := client.Repositories.ListTags(ctx, owner, repo, opts)
		if err != nil {
			return resolvedRef{}, fmt.Errorf("listing tags for %s/%s: %w", owner, repo, err)
		}
		for _, tag := range tags {
			if isNewerTag(tag.GetName(), latest.GetName()) {
				latest = tag
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	if latest == nil {
		return resolvedRef{}, fmt.Errorf("%s/%s: %w", owner, repo, errNoSemverTags)
	}
	return resolvedRef{sha: latest.Commit.GetSHA(), version: latest.GetName()}, nil
}

// isNewerTag reports whether name is a stable semver tag above current. Of two
// equal versions the more specific one wins, so v2.0.0 is preferred over v2.
func isNewerTag(name, current string) bool {
	version, ok := stableSemver(name)
	if !ok {
		return false
	}
	currentVersion, ok := stableSemver(current)
	if !ok {
		return true
	}
	cmp := semver.Compare(version, currentVersion)
	return cmp > 0 || (cmp == 0 && len(name) > len(current))
}

// stableSemver returns the tag in the v-prefixed form the semver package expects.
// Named tags and prereleases are not stable versions.
func stableSemver(tag string) (string, bool) {
	version := withVPrefix(tag)
	if !semver.IsValid(version) || semver.Prerelease(version) != "" {
		return "", false
	}
	return version, true
}

func withVPrefix(tag string) string {
	if strings.HasPrefix(tag, "v") {
		return tag
	}
	return "v" + tag
}
