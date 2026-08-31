package main

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseGoOptions(t *testing.T) {
	t.Parallel()
	opts, err := parseGoOptions("test", []string{"--version", "go1.27.1", "--dry-run", "--system"}, new(bytes.Buffer))
	if err != nil {
		t.Fatalf("parseGoOptions() error = %v", err)
	}
	if opts.version != "go1.27.1" || !opts.dryRun || !opts.systemPath {
		t.Fatalf("parseGoOptions() = %#v", opts)
	}
}

func TestParseToolOptionsRejectsVersion(t *testing.T) {
	t.Parallel()
	if _, err := parseToolOptions("test", []string{"--version", "2.18.0"}, new(bytes.Buffer)); err == nil {
		t.Fatal("parseToolOptions() error = nil, want unsupported flag")
	}
}

func TestParseAllOptionsRejectsGoVersion(t *testing.T) {
	t.Parallel()
	if _, err := parseAllOptions([]string{"--version", "go1.27.1"}, new(bytes.Buffer)); err == nil {
		t.Fatal("parseAllOptions() error = nil, want unsupported flag")
	}
}

func TestRunAllDryRunOrdersToolResolutionWithoutSideEffects(t *testing.T) {
	t.Parallel()
	osRelease := filepath.Join(t.TempDir(), "os-release")
	writeTestFile(t, osRelease, []byte("ID=kali\nID_LIKE=debian\n"), 0o600)
	goreleaserAsset, _ := goreleaserNativeAsset("2.18.0", formatDeb, "amd64")
	lintAsset, _ := golangCILintNativeAsset("2.13.2", formatDeb, "amd64")
	releases := &fakeReleaseService{releases: map[string]release{
		"goreleaser/goreleaser": {
			TagName: "v2.18.0",
			Assets:  []releaseAsset{{Name: goreleaserAsset}, {Name: "checksums.txt"}},
		},
		"golangci/golangci-lint": {
			TagName: "v2.13.2",
			Assets:  []releaseAsset{{Name: lintAsset}, {Name: "golangci-lint-2.13.2-checksums.txt"}},
		},
	}}
	runner := &fakeRunner{
		paths: map[string]string{
			"go":            "/usr/local/go/bin/go",
			"goreleaser":    "/usr/bin/goreleaser",
			"golangci-lint": "/usr/bin/golangci-lint",
			"dpkg":          "/usr/bin/dpkg",
		},
		outputs: map[string][]byte{
			"/usr/local/go/bin/go":   []byte("go version go1.27.0 linux/amd64"),
			"/usr/bin/goreleaser":    []byte("goreleaser version 2.17.0"),
			"/usr/bin/golangci-lint": []byte("golangci-lint has version 2.12.0"),
		},
	}
	app := testApplication(t, releases, runner)
	app.osReleasePath = osRelease
	app.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "go1.27.0\ntime 2026-08-05T00:00:00Z\n"), nil
	})}
	if err := app.run(context.Background(), []string{"all", "--dry-run"}); err != nil {
		t.Fatalf("run(all) error = %v", err)
	}
	wantCalls := []string{"goreleaser/goreleaser", "golangci/golangci-lint"}
	if !reflect.DeepEqual(releases.latestCalls, wantCalls) {
		t.Fatalf("release order = %v, want %v", releases.latestCalls, wantCalls)
	}
	if releases.downloadCalls != 0 || len(runner.runs) != 0 {
		t.Fatalf("all dry run caused side effects: downloads=%d runs=%v", releases.downloadCalls, runner.runs)
	}
}
