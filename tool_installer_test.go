package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeReleaseService struct {
	release       release
	releases      map[string]release
	latestErr     error
	downloads     map[string][]byte
	downloadCalls int
	latestCalls   []string
}

func (f *fakeReleaseService) Latest(_ context.Context, repository string) (release, error) {
	f.latestCalls = append(f.latestCalls, repository)
	if selected, found := f.releases[repository]; found {
		return selected, f.latestErr
	}
	return f.release, f.latestErr
}

func (f *fakeReleaseService) Download(_ context.Context, downloadURL string, output io.Writer, _ int64) error {
	f.downloadCalls++
	data, found := f.downloads[downloadURL]
	if !found {
		return fmt.Errorf("unexpected download %s", downloadURL)
	}
	_, err := output.Write(data)
	return err
}

type runCall struct {
	root bool
	name string
	args []string
}

type fakeRunner struct {
	paths   map[string]string
	outputs map[string][]byte
	runs    []runCall
}

func (f *fakeRunner) LookPath(file string) (string, error) {
	path, found := f.paths[file]
	if !found {
		return "", os.ErrNotExist
	}
	return path, nil
}

func (f *fakeRunner) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	output, found := f.outputs[name]
	if !found {
		return nil, os.ErrNotExist
	}
	return output, nil
}

func (f *fakeRunner) Run(_ context.Context, root bool, name string, args ...string) error {
	f.runs = append(f.runs, runCall{root: root, name: name, args: append([]string(nil), args...)})
	return nil
}

func TestInstallToolSkipsMatchingVersion(t *testing.T) {
	t.Parallel()
	releases := &fakeReleaseService{release: release{TagName: "v2.18.0"}}
	runner := &fakeRunner{
		paths:   map[string]string{"goreleaser": "/usr/bin/goreleaser"},
		outputs: map[string][]byte{"/usr/bin/goreleaser": []byte("goreleaser version 2.18.0")},
	}
	app := testApplication(t, releases, runner)
	if err := app.installTool(context.Background(), goreleaserSpec(), toolOptions{}); err != nil {
		t.Fatalf("installTool() error = %v", err)
	}
	if releases.downloadCalls != 0 || len(runner.runs) != 0 {
		t.Fatalf("matching version caused side effects: downloads=%d runs=%v", releases.downloadCalls, runner.runs)
	}
}

func TestInstallToolSkipsWhenCurrentPackageIsShadowed(t *testing.T) {
	t.Parallel()
	releases := &fakeReleaseService{release: release{TagName: "v2.18.0"}}
	runner := &fakeRunner{
		paths: map[string]string{"goreleaser": "/usr/local/bin/goreleaser"},
		outputs: map[string][]byte{
			"/usr/local/bin/goreleaser": []byte("goreleaser version 2.17.0"),
			"/usr/bin/goreleaser":       []byte("goreleaser version 2.18.0"),
		},
	}
	app := testApplication(t, releases, runner)
	if err := app.installTool(context.Background(), goreleaserSpec(), toolOptions{}); err != nil {
		t.Fatalf("installTool() error = %v", err)
	}
	if releases.downloadCalls != 0 || len(runner.runs) != 0 {
		t.Fatalf("shadowed current version caused side effects: downloads=%d runs=%v", releases.downloadCalls, runner.runs)
	}
	stderr, ok := app.stderr.(*bytes.Buffer)
	if !ok {
		t.Fatalf("stderr type = %T, want *bytes.Buffer", app.stderr)
	}
	if !strings.Contains(stderr.String(), "PATH resolves") {
		t.Fatalf("shadowing warning missing: %s", stderr)
	}
}

func TestInstallToolDryRunSelectsDebWithoutSideEffects(t *testing.T) {
	t.Parallel()
	osRelease := filepath.Join(t.TempDir(), "os-release")
	writeTestFile(t, osRelease, []byte("ID=kali\nID_LIKE=debian\n"), 0o600)
	spec := goreleaserSpec()
	assetName, _ := spec.NativeAsset("2.18.0", formatDeb, "amd64")
	releases := &fakeReleaseService{release: release{
		TagName: "v2.18.0",
		Assets: []releaseAsset{
			{Name: assetName, DownloadURL: "https://github.com/asset"},
			{Name: "checksums.txt", DownloadURL: "https://github.com/checksums"},
		},
	}}
	runner := &fakeRunner{
		paths:   map[string]string{"goreleaser": "/usr/bin/goreleaser", "dpkg": "/usr/bin/dpkg"},
		outputs: map[string][]byte{"/usr/bin/goreleaser": []byte("goreleaser version 2.17.0")},
	}
	app := testApplication(t, releases, runner)
	app.osReleasePath = osRelease
	if err := app.installTool(context.Background(), spec, toolOptions{dryRun: true}); err != nil {
		t.Fatalf("installTool() error = %v", err)
	}
	if releases.downloadCalls != 0 || len(runner.runs) != 0 {
		t.Fatalf("dry run caused side effects: downloads=%d runs=%v", releases.downloadCalls, runner.runs)
	}
	output, ok := app.stdout.(*bytes.Buffer)
	if !ok {
		t.Fatalf("stdout type = %T, want *bytes.Buffer", app.stdout)
	}
	if !strings.Contains(output.String(), assetName) {
		t.Fatalf("dry-run output does not contain asset %q: %s", assetName, app.stdout)
	}
}

func TestSelectToolAssetFallsBackToArchive(t *testing.T) {
	t.Parallel()
	spec := golangCILintSpec()
	archiveName, _ := spec.ArchiveAsset("2.13.2", "amd64")
	app := testApplication(t, &fakeReleaseService{}, &fakeRunner{paths: map[string]string{"pacman": "/usr/bin/pacman"}})
	selection, err := app.selectToolAsset(spec, "2.13.2", release{Assets: []releaseAsset{{Name: archiveName}}}, linuxPlatform{Format: formatArch, Arch: "amd64"})
	if err != nil {
		t.Fatalf("selectToolAsset() error = %v", err)
	}
	if selection.Format != formatArchive || selection.Asset.Name != archiveName {
		t.Fatalf("selectToolAsset() = %#v", selection)
	}
}

func TestInstallNativePackageCommands(t *testing.T) {
	t.Parallel()
	tests := []struct {
		format   packageFormat
		name     string
		wantArgs []string
	}{
		{format: formatDeb, name: "dpkg", wantArgs: []string{"--install", "/tmp/tool.deb"}},
		{format: formatRPM, name: "rpm", wantArgs: []string{"--upgrade", "--replacepkgs", "/tmp/tool.deb"}},
		{format: formatAPK, name: "apk", wantArgs: []string{"add", "--allow-untrusted", "/tmp/tool.deb"}},
		{format: formatArch, name: "pacman", wantArgs: []string{"-U", "--noconfirm", "/tmp/tool.deb"}},
	}
	for _, test := range tests {
		t.Run(string(test.format), func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{}
			app := testApplication(t, &fakeReleaseService{}, runner)
			if err := app.installNativePackage(context.Background(), test.format, "/tmp/tool.deb"); err != nil {
				t.Fatalf("installNativePackage() error = %v", err)
			}
			if len(runner.runs) != 1 || !runner.runs[0].root || runner.runs[0].name != test.name || strings.Join(runner.runs[0].args, " ") != strings.Join(test.wantArgs, " ") {
				t.Fatalf("installNativePackage() calls = %#v", runner.runs)
			}
		})
	}
}

func TestDownloadVerifiedAsset(t *testing.T) {
	t.Parallel()
	assetData := []byte("verified release payload")
	digest := sha256.Sum256(assetData)
	releases := &fakeReleaseService{downloads: map[string][]byte{
		"checksums": []byte(fmt.Sprintf("%x  tool.tar.gz\n", digest)),
		"asset":     assetData,
	}}
	app := testApplication(t, releases, &fakeRunner{})
	path, err := app.downloadVerifiedAsset(context.Background(), releaseAsset{Name: "tool.tar.gz", DownloadURL: "asset"}, releaseAsset{DownloadURL: "checksums"}, t.TempDir())
	if err != nil {
		t.Fatalf("downloadVerifiedAsset() error = %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // Test-owned path.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, assetData) {
		t.Fatalf("downloaded data = %q, want %q", got, assetData)
	}
}

func TestDownloadVerifiedAssetRejectsMismatch(t *testing.T) {
	t.Parallel()
	digest := sha256.Sum256([]byte("expected"))
	releases := &fakeReleaseService{downloads: map[string][]byte{
		"checksums": []byte(fmt.Sprintf("%x  tool.tar.gz\n", digest)),
		"asset":     []byte("tampered"),
	}}
	app := testApplication(t, releases, &fakeRunner{})
	_, err := app.downloadVerifiedAsset(context.Background(), releaseAsset{Name: "tool.tar.gz", DownloadURL: "asset"}, releaseAsset{DownloadURL: "checksums"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("downloadVerifiedAsset() error = %v, want mismatch", err)
	}
}

func TestChecksumForAsset(t *testing.T) {
	t.Parallel()
	digest := sha256.Sum256([]byte("asset"))
	got, err := checksumForAsset(fmt.Sprintf("%x  other\n%x *wanted\n", digest, digest), "wanted")
	if err != nil {
		t.Fatalf("checksumForAsset() error = %v", err)
	}
	if !bytes.Equal(got, digest[:]) {
		t.Fatalf("checksumForAsset() = %x, want %x", got, digest)
	}
}

func TestExtractBinary(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "tool.tar.gz")
	writeTestFile(t, archive, makeTarGzip(t, []tarEntry{{name: "tool-1.2.3/tool", body: []byte("binary")}}), 0o600)
	destination := filepath.Join(t.TempDir(), "tool")
	if err := extractBinary(archive, "tool", destination); err != nil {
		t.Fatalf("extractBinary() error = %v", err)
	}
	got, err := os.ReadFile(destination) //nolint:gosec // Test-owned path.
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("extracted binary = %q", got)
	}
}

func TestExtractBinaryRejectsTraversal(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "tool.tar.gz")
	writeTestFile(t, archive, makeTarGzip(t, []tarEntry{{name: "../tool", body: []byte("binary")}}), 0o600)
	err := extractBinary(archive, "tool", filepath.Join(t.TempDir(), "tool"))
	if err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("extractBinary() error = %v, want unsafe path", err)
	}
}

func testApplication(t *testing.T, releases releaseService, runner commandRunner) *application {
	t.Helper()
	return &application{
		stdout:        new(bytes.Buffer),
		stderr:        new(bytes.Buffer),
		releases:      releases,
		runner:        runner,
		goos:          "linux",
		goarch:        "amd64",
		tempDir:       t.TempDir,
		osReleasePath: "/does/not/exist",
	}
}

func writeTestFile(t testing.TB, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

type tarEntry struct {
	name string
	body []byte
}

func makeTarGzip(t testing.TB, entries []tarEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestChecksumForAssetMissing(t *testing.T) {
	t.Parallel()
	_, err := checksumForAsset("", "missing")
	if err == nil {
		t.Fatal("checksumForAsset() error = nil")
	}
}
