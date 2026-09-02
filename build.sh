#!/bin/sh
# The banner must name the exact code the binary was built from: a stale
# binary attributing a live session to the wrong commit invalidates every
# measurement it produces. describe gives the tag and dirty state,
# rev-parse gives the full SHA that -version and the banner print verbatim.
go build -ldflags "-X main.version=$(git describe --tags --always --dirty) -X main.commit=$(git rev-parse HEAD)" -o ag ./cmd/ag
