package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxGoArchive = 512 << 20

func (a *application) installGo(ctx context.Context, opts goOptions) error {
	version := cleanVersionInput(opts.version)
	if version == "" {
		var err error
		version, err = a.fetchLatestGoVersion(ctx)
		if err != nil {
			return fmt.Errorf("fetch latest Go version: %w", err)
		}
	}
	if !validGoVersion(version) {
		return fmt.Errorf("invalid Go version %q", version)
	}

	if installed, err := a.installedGoVersion(ctx); err == nil && installed == version {
		a.log("Go is already up to date (%s). Nothing to do.", version)
		return nil
	}
	goos, goarch, err := resolveGoTarget(a.goos, a.goarch)
	if err != nil {
		return err
	}

	archiveName := fmt.Sprintf("%s.%s-%s.tar.gz", version, goos, goarch)
	downloadURL := "https://go.dev/dl/" + archiveName
	downloadDir := opts.downloadDir
	if downloadDir == "" {
		downloadDir = a.tempDir()
	}
	if err := os.MkdirAll(downloadDir, 0o750); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}
	archivePath := filepath.Join(downloadDir, archiveName)

	a.log("Target version: %s", version)
	a.log("Platform: %s/%s", goos, goarch)
	a.log("Download: %s\n       to: %s", downloadURL, archivePath)
	if opts.dryRun {
		a.printGoPlan(version, goos, downloadURL, archivePath, opts)
		return nil
	}

	if _, err := os.Stat(archivePath); err == nil {
		a.log("Using existing archive: %s", archivePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Go archive: %w", err)
	} else {
		if err := a.downloadGoArchive(ctx, downloadURL, archivePath); err != nil {
			return fmt.Errorf("download Go archive: %w", err)
		}
		a.log("Downloaded: %s", archivePath)
	}

	if err := a.runner.Run(ctx, true, "rm", "-rf", "/usr/local/go"); err != nil {
		return fmt.Errorf("remove previous /usr/local/go: %w", err)
	}
	if err := a.runner.Run(ctx, true, "tar", "-C", "/usr/local", "-xzf", archivePath); err != nil {
		return fmt.Errorf("extract Go archive to /usr/local: %w", err)
	}
	a.log("Extracted to /usr/local/go")

	if !opts.noPathUpdate {
		if err := a.ensureUserGoPath(); err != nil {
			return fmt.Errorf("ensure user Go PATH: %w", err)
		}
		if opts.systemPath {
			if err := a.ensureSystemGoPath(ctx); err != nil {
				return fmt.Errorf("ensure system Go PATH: %w", err)
			}
		}
	}

	output, err := a.runner.Output(ctx, "/usr/local/go/bin/go", "version")
	if err != nil {
		return fmt.Errorf("verify Go installation: %w", err)
	}
	installed, err := parseGoVersionOutput(string(output))
	if err != nil {
		return fmt.Errorf("verify Go installation: %w", err)
	}
	if installed != version {
		return fmt.Errorf("verify Go installation: expected %s, got %s", version, installed)
	}
	writeBestEffort(a.stdout, "%s", output)
	a.log("Go %s installed successfully.", version)
	return nil
}

func (a *application) fetchLatestGoVersion(ctx context.Context) (string, error) {
	const versionURL = "https://go.dev/VERSION?m=text"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := a.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s from %s", response.Status, versionURL)
	}
	reader := bufio.NewReader(io.LimitReader(response.Body, 1024))
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	version := strings.TrimSpace(line)
	if !validGoVersion(version) {
		return "", fmt.Errorf("invalid version string %q", version)
	}
	return version, nil
}

func resolveGoTarget(goos, goarch string) (string, string, error) {
	switch goos {
	case "linux", "darwin":
	default:
		return "", "", fmt.Errorf("unsupported OS %q for Go installation", goos)
	}
	switch goarch {
	case "amd64", "arm64":
		return goos, goarch, nil
	default:
		return "", "", fmt.Errorf("unsupported architecture %q for Go installation", goarch)
	}
}

func (a *application) downloadGoArchive(ctx context.Context, downloadURL, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := a.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", response.Status)
	}

	directory := filepath.Dir(destination)
	partial, err := os.CreateTemp(directory, ".go-download-*")
	if err != nil {
		return err
	}
	partialPath := partial.Name()
	defer func() {
		_ = os.Remove(partialPath)
	}()
	if response.ContentLength > maxGoArchive {
		_ = partial.Close()
		return fmt.Errorf("go archive is too large: %d bytes", response.ContentLength)
	}
	limited := &io.LimitedReader{R: response.Body, N: maxGoArchive + 1}
	written, copyErr := io.Copy(partial, limited)
	closeErr := partial.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 {
		return errors.New("downloaded Go archive is empty")
	}
	if written > maxGoArchive {
		return fmt.Errorf("go archive exceeds %d bytes", maxGoArchive)
	}
	return os.Rename(partialPath, destination)
}

func (a *application) ensureUserGoPath() error {
	home, err := a.userHomeDir()
	if err != nil {
		return err
	}
	line := "export PATH=$PATH:/usr/local/go/bin"
	candidates := []string{".profile"}
	if a.goos == "darwin" {
		candidates = []string{".zprofile", ".zshrc", ".bash_profile", ".profile"}
	}

	for _, name := range candidates {
		path := filepath.Join(home, name)
		data, readErr := os.ReadFile(path) //nolint:gosec // Fixed profile name under the user home.
		if readErr == nil && containsProfileLine(string(data), line) {
			a.log("User PATH already contains /usr/local/go/bin in %s", path)
			return nil
		}
	}

	target := filepath.Join(home, candidates[0])
	for _, name := range candidates {
		candidate := filepath.Join(home, name)
		if _, statErr := os.Stat(candidate); statErr == nil {
			target = candidate
			break
		}
	}
	content := []byte("\n# Added by go-updater to expose Go binaries\n" + line + "\n")
	file, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // Profiles must be user-readable.
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	a.log("Added PATH update to %s", target)
	return nil
}

func containsProfileLine(content, target string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == target || (strings.Contains(line, "/usr/local/go/bin") && strings.Contains(line, "export PATH")) {
			return true
		}
	}
	return false
}

func (a *application) ensureSystemGoPath(ctx context.Context) error {
	content := []byte("# Added by go-updater\nexport PATH=\"$PATH:/usr/local/go/bin\"\n")
	target := "/etc/profile.d/golang-path.sh"
	if a.goos == "darwin" {
		content = []byte("/usr/local/go/bin\n")
		target = "/etc/paths.d/go"
	}

	temporary, err := os.CreateTemp(a.tempDir(), "golang-path-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := a.runner.Run(ctx, true, "install", "-m", "0644", temporaryPath, target); err != nil {
		return err
	}
	a.log("Added system PATH at %s", target)
	return nil
}

func (a *application) installedGoVersion(ctx context.Context) (string, error) {
	paths := []string{"/usr/local/go/bin/go"}
	if path, err := a.runner.LookPath("go"); err == nil {
		paths = append(paths, path)
	}
	for _, path := range paths {
		output, err := a.runner.Output(ctx, path, "version")
		if err != nil {
			continue
		}
		if version, err := parseGoVersionOutput(string(output)); err == nil {
			return version, nil
		}
	}
	return "", errors.New("no installed Go version found")
}

func parseGoVersionOutput(output string) (string, error) {
	for token := range strings.FieldsSeq(strings.TrimSpace(output)) {
		if strings.HasPrefix(token, "go") {
			version := cleanVersionInput(token)
			if validGoVersion(version) {
				return version, nil
			}
		}
	}
	return "", fmt.Errorf("unable to parse version from %q", output)
}

func cleanVersionInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	for token := range strings.FieldsSeq(input) {
		input = token
		break
	}
	if !strings.HasPrefix(input, "go") {
		input = "go" + input
	}
	var output bytes.Buffer
	for index := range len(input) {
		character := input[index]
		if index < 2 || (character >= '0' && character <= '9') || (character >= 'a' && character <= 'z') || character == '.' {
			output.WriteByte(character)
			continue
		}
		break
	}
	return output.String()
}

func validGoVersion(version string) bool {
	if !strings.HasPrefix(version, "go") || len(version) <= 2 {
		return false
	}
	hasDigit := false
	for _, character := range version[2:] {
		if character >= '0' && character <= '9' {
			hasDigit = true
			continue
		}
		if character == '.' || (character >= 'a' && character <= 'z') {
			continue
		}
		return false
	}
	return hasDigit
}

func (a *application) printGoPlan(version, goos, downloadURL, archivePath string, opts goOptions) {
	writeBestEffort(a.stdout, "Plan (dry-run):\n")
	writeBestEffort(a.stdout, "- Determine version: %s\n", version)
	writeBestEffort(a.stdout, "- Download %s -> %s\n", downloadURL, archivePath)
	writeBestEffort(a.stdout, "- Remove any previous /usr/local/go\n")
	writeBestEffort(a.stdout, "- Extract archive into /usr/local\n")
	if opts.noPathUpdate {
		writeBestEffort(a.stdout, "- Skip PATH update (per --no-path-update)\n")
	} else {
		writeBestEffort(a.stdout, "- Add /usr/local/go/bin to the user PATH idempotently\n")
		if opts.systemPath {
			if goos == "darwin" {
				writeBestEffort(a.stdout, "- Add system PATH via /etc/paths.d/go\n")
			} else {
				writeBestEffort(a.stdout, "- Add system PATH via /etc/profile.d/golang-path.sh\n")
			}
		}
	}
	writeBestEffort(a.stdout, "- Verify with /usr/local/go/bin/go version\n")
}
