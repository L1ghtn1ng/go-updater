# Go Updater

Installer automates installing the latest Go per https://go.dev/doc/install

## Steps

1. Download the latest version archive for your OS/ARCH from go.dev.
2. Remove any previous /usr/local/go directory (requires sudo/root).
3. Extract the archive into /usr/local (requires sudo/root).
4. Ensure /usr/local/go/bin is on PATH by adding to $HOME/.profile (idempotent).
5. Verify with `go version`.

## Usage examples

```sh
go run .                 # install latest
go run . --version go1.26.0
go run . --dry-run       # show what would be done
go run . --system        # also add a system-wide PATH entry (requires sudo)
```
