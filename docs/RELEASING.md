# Releasing nabd

Checklist for a person who did not write this repository.
The installable binary is `nabd`. The package path remains `./cmd/ag`.

`v1.5.0` is already tagged. Do not move or retag `v1.5.0`; every correction
must use a new tag.

## 0. One-time repository metadata

Description is what it is, not why it is nice:

```sh
gh repo edit amiraq1/Nabd \
  --description "Terminal coding agent: append-only session journal, default-deny permissions, single static binary." \
  --add-topic golang \
  --add-topic cli \
  --add-topic terminal \
  --add-topic agent \
  --add-topic android \
  --add-topic termux
```

## 1. Gates on the commit you will tag

```sh
test -z "$(gofmt -l .)"
go vet ./...
staticcheck ./...          # v0.8.1
go test ./... -race -count=1
TMPDIR=${TMPDIR:-$(mktemp -d)}
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$TMPDIR/nabd-linux-amd64" ./cmd/ag
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/ag
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./cmd/ag
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./cmd/ag
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build ./cmd/ag
```

Windows is not a release target (`syscall.Kill`, `Setpgid`).

## 2. Tag

Choose a new semantic version; never reuse an existing tag:

```sh
VERSION=v1.5.1
git checkout master
git pull --ff-only
git tag -a "$VERSION" -m "nabd $VERSION"
git push origin "$VERSION"
```

Pushing `v*` runs `.github/workflows/release.yml`, which runs
`goreleaser release --clean`. The pipeline publishes five static, trimpath,
ldflags-stamped `nabd` binaries, one distinct Syft SBOM per binary,
`checksums.txt`, and the checksum signature and certificate. SBOMs are included
in `checksums.txt`; signing that checksum transitively covers every listed
binary and SBOM. The workflow also attaches a build-provenance attestation to
`dist/checksums.txt`.

## 3. Verify and smoke the artifacts

On a machine that does not have the repository, download the binary for the
platform together with `checksums.txt`, `checksums.txt.sig`, and
`checksums.txt.pem`. Then verify the signed checksum manifest before trusting
its entries:

```sh
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github\.com/amiraq1/Nabd/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
sha256sum --check --ignore-missing checksums.txt
```

Smoke the downloaded binary:

```sh
chmod +x nabd_1.5.1_linux_amd64
./nabd_1.5.1_linux_amd64 --version
```

Accept when the banner names version, commit, and date. A `dev · none` binary
is a local `go build`, not a release.

## 4. Local stamp (not a release)

```sh
./build.sh
./nabd --version
```
