# Releasing nabd

Checklist for a person who did not write this repository.
The installable binary is `nabd`. The package path remains `./cmd/ag`.
The repository target is exclusively `android/arm64` (Termux).

`v2.1.0` is already tagged. Do not move or retag any existing version tag; every correction
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

Pre-release validation must pass all quality and security invariants for `android/arm64`:

```sh
test -z "$(gofmt -l .)"
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go vet ./...
GOOS=android GOARCH=arm64 staticcheck ./...          # v0.8.1
go test ./... -race -count=1
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build ./cmd/ag
bash scripts/check-threat-model-tests.sh
bash scripts/check-security-invariants.sh
```

Desktop operating systems (Linux desktop, macOS, Windows) are not release targets.
`v2.0.0` and later target Termux on `android/arm64` exclusively.

## 2. Tag

Choose a new semantic version; never reuse an existing tag:

```sh
VERSION=v2.1.1
git checkout master
git pull --ff-only
git tag -a "$VERSION" -m "nabd $VERSION"
git push origin "$VERSION"
```

Pushing `v*` runs `.github/workflows/release.yml`, which runs
`goreleaser release --clean`. The pipeline publishes:
1. One static, trimpath, ldflags-stamped `android/arm64` binary: `nabd_{{ .Version }}_android_arm64`.
2. One Syft SBOM: `nabd_{{ .Version }}_android_arm64.sbom.json`.
3. Checksum manifest: `checksums.txt`.
4. Cosign keyless signature and certificate: `checksums.txt.sig` and `checksums.txt.pem`.

The SBOM is listed inside `checksums.txt`; signing `checksums.txt` transitively
covers both the binary and the SBOM. The release workflow also attaches a
build-provenance attestation to `dist/checksums.txt`.

## 3. Verify and smoke the artifacts

On a test machine or Termux environment, download the release artifacts:

```sh
V=2.1.1
BASE="https://github.com/amiraq1/Nabd/releases/download/v${V}"
curl -LO "${BASE}/nabd_${V}_android_arm64" \
     -LO "${BASE}/nabd_${V}_android_arm64.sbom.json" \
     -LO "${BASE}/checksums.txt" \
     -LO "${BASE}/checksums.txt.sig" \
     -LO "${BASE}/checksums.txt.pem"
```

Verify the signed checksum manifest using Cosign keyless verification:

```sh
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github\.com/amiraq1/Nabd/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

sha256sum --check --ignore-missing checksums.txt
```

Smoke the downloaded binary on an `android/arm64` device:

```sh
chmod +x "nabd_${V}_android_arm64"
"./nabd_${V}_android_arm64" --version
```

Accept when the banner names the expected version, commit SHA, and date.
A `dev · none` banner indicates an unreleased local build.

## 4. Local stamp (not a release)

For local development builds on Termux:

```sh
./build.sh
./nabd --version
```
