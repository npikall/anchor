_default:
    @just --list

alias b := build
alias i := install
alias l := lint
alias t := test

commit := " -X main.commit=" + `git rev-parse --short HEAD`
version := " -X main.version=dev-" + `date -u +%Y-%m`
ldflags := "-s -w" + commit + version

# build the binary
build:
    go build -ldflags="{{ ldflags }}"

# install the binary
install:
    go install -ldflags="{{ ldflags }}"

# run the tests
test:
    gotestsum ./...

# run the linter
lint *args:
    golangci-lint run {{ args }}
