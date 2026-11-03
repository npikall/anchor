# anchor

Pin GitHub Actions `uses:` directives to their exact commit SHA.

```yaml
# before
- uses: actions/checkout@v4

# after
- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4
```

## Install

```sh
go install github.com/npikall/anchor@latest
```

## Usage

```sh
anchor [flags] <file-or-directory>...
```

Directories are searched recursively for `.yml` and `.yaml` files, skipping `.git`.
Each action is requested only once, even if multiple files use it.

| Flag | Description |
|------|-------------|
| `-u`, `--update` | Pin to highest stable semver tag instead of current ref |
| `-i`, `--in-place` | Overwrite files in place |
| `-v`, `--verbose` | Warn about skipped local/docker actions |
| `-n`, `--workers` | Number of concurrent workers |

Print pinned workflow to stdout:

```sh
anchor .github/workflows/ci.yml
```

Pin in place:

```sh
anchor -i .github/workflows/ci.yml
```

Update all actions to their latest tag:

```sh
anchor -u -i .github/workflows/ci.yml
```

Pin all workflows and composite actions in place:

```sh
anchor -i .github
```

Pin multiple files:

```sh
anchor -i .github/workflows/ci.yml .github/workflows/release.yml
```

`--update` ignores named tags (`zola`) and prereleases (`v3.0.0-rc.1`). Actions without a semver tag stay unchanged and print a warning. Repositories with many tags need several API requests, so set `GITHUB_TOKEN` to avoid rate limits.

When printing multiple files to stdout, each file is preceded by a `# <path>` comment.
