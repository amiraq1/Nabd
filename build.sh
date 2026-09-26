#!/bin/sh
# The banner must name the exact code the binary was built from: a stale
# binary attributing a live session to the wrong commit invalidates every
# measurement it produces. describe gives the tag and dirty state,
# rev-parse gives the full SHA, date is UTC so two builds of the same
# commit still differ if they are not the same release artifact.
# Output name is nabd (package path remains ./cmd/ag; ag collides with
# the_silver_searcher).
go build -trimpath -ldflags "-X nabd/internal/build.version=$(git describe --tags --always --dirty) -X nabd/internal/build.commit=$(git rev-parse HEAD) -X nabd/internal/build.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o nabd ./cmd/ag
