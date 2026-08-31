# Go Updater

`go-updater` installs the latest stable Go release and selected Go developer
tools. It supports verified, distro-aware installation of
[GoReleaser](https://github.com/goreleaser/goreleaser/releases) and
[golangci-lint](https://github.com/golangci/golangci-lint/releases).

## Usage

The original no-argument behavior remains unchanged: it updates Go.

```sh
go-updater
go-updater go --version go1.27.1
go-updater goreleaser
go-updater golangci-lint
go-updater all
```

Every command supports `--dry-run`. Use `--download-dir DIR` to retain release
assets in a specific directory. The Go command also supports
`--no-path-update`, `--system`, and an exact `--version` override.

The GoReleaser and golangci-lint commands always target the latest stable
release. They query GitHub before doing any privileged work and exit without
downloading or invoking `sudo` when the installed version already matches.

For authenticated GitHub API requests, set `GITHUB_TOKEN` in the environment.
The token is never printed.

## Linux package selection

The tool installers read `/etc/os-release` and select an upstream package for
the running architecture:

| Distribution family | Package | Installer |
| --- | --- | --- |
| Debian, Ubuntu, Kali | `.deb` | `dpkg --install` |
| Fedora, RHEL, SUSE | `.rpm` | `rpm --upgrade --replacepkgs` |
| Alpine | `.apk` | `apk add --allow-untrusted` |
| Arch Linux | `.pkg.tar.zst` | `pacman -U --noconfirm` |

If the project does not publish a native package for the detected combination,
the updater falls back to its Linux archive and installs the binary in
`/usr/local/bin`. This is the normal path for golangci-lint on Alpine and Arch.

The selected asset is checked against the SHA-256 manifest from the same GitHub
release before it is passed to a package manager or extracted. Archive fallback
accepts only the expected regular-file binary and rejects links, traversal
paths, duplicate binaries, and oversized entries.

GoReleaser and golangci-lint installation is Linux-only and supports `amd64`
and `arm64`. Go installation continues to support Linux and macOS on those
architectures.

## Go installation

The Go installer:

1. Resolves the latest version from `go.dev`, unless `--version` is supplied.
2. Skips installation when that exact version is already installed.
3. Downloads the matching OS/architecture archive.
4. Replaces `/usr/local/go` using `sudo` or the current root session.
5. Adds `/usr/local/go/bin` to the user profile idempotently unless disabled.
6. Verifies the installed version through `/usr/local/go/bin/go version`.

Use `--system` to also install a path entry under `/etc/profile.d` on Linux or
`/etc/paths.d` on macOS.

## Development

The project targets the Go 1.27 release line.

```sh
make fix
make fmt-check
make lint
make test
make vet
make build
make snapshot
```
