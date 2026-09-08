# Releasing nabd

Checklist for a person who did not write this repository.
The installable binary is `nabd`. The package path remains `./cmd/ag`.

`v1.2.0` is already tagged. The next release that uses this pipeline is **v1.3.0**.
Do not move or retag `v1.2.0`.

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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/nabd-linux-amd64 ./cmd/ag
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/ag
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./cmd/ag
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./cmd/ag
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build ./cmd/ag
```

Windows is not a release target (`syscall.Kill`, `Setpgid`).

## 2. Tag

```sh
git checkout master
git pull
git tag -a v1.3.0 -m "nabd v1.3.0"
git push origin v1.3.0
```

Pushing `v*` runs `.github/workflows/release.yml`, which runs
`goreleaser release --clean`. That publishes static, trimpath,
ldflags-stamped `nabd` binaries plus `checksums.txt`.

## 3. Smoke the artifact

On a machine that does not have the repo:

```sh
curl -L -o nabd https://github.com/amiraq1/Nabd/releases/download/v1.3.0/nabd_1.3.0_linux_amd64
chmod +x nabd
./nabd --version
```

Accept when the banner names version, commit, and date. A `dev · none`
binary is a local `go build`, not a release.

## 4. Local stamp (not a release)

```sh
./build.sh
./nabd --version
```
