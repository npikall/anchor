_default:
    @just --list

ldflags := "-s -w"

# build the binary
build:
    go build -ldflags="{{ ldflags }}"

# install the binary
install:
    go install -ldflags="{{ ldflags }}"
