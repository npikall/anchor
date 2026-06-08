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
anchor [flags] <workflow.yml>
```

| Flag | Description |
|------|-------------|
| `-u` | Pin to latest tag instead of current ref |
| `-i` | Overwrite file in place |
| `-v` | Warn about skipped local/docker actions |

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
