#!/bin/sh
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" -o ag ./cmd/ag
