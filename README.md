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
| `-r`, `--refresh` | Request all actions again instead of reusing cached hashes |
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

Resolved hashes are cached in `anchor/anchor.db` in the user data directory, so later runs need fewer requests: `$XDG_DATA_HOME` if set, otherwise `~/.local/share` on Linux, `~/Library/Application Support` on macOS and `%AppData%` on Windows. Exact versions (`v4.2.2`) are reused indefinitely. Everything that can move (`v4`, `main`, the latest tag of `--update`) is requested again after 24 hours. Use `--refresh` to request everything now, for example when a tag was moved. If the cache cannot be used, anchor prints a warning and continues without it.

When printing multiple files to stdout, each file is preceded by a `# <path>` comment.

While actions are resolved, a progress bar is shown on stderr if it is a terminal. It disappears when done and never ends up in redirected output.
