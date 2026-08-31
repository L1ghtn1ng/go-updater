package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestGitHubReleaseClientLatest(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		body := `{"tag_name":"v2.18.0","assets":[{"name":"checksums.txt","browser_download_url":"https://github.com/checksums"}]}`
		return response(http.StatusOK, body), nil
	})
	client := newGitHubReleaseClient(&http.Client{Transport: transport}, "secret")
	got, err := client.Latest(context.Background(), "goreleaser/goreleaser")
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if got.TagName != "v2.18.0" || len(got.Assets) != 1 {
		t.Fatalf("Latest() = %#v", got)
	}
}

func TestGitHubReleaseClientRejectsRepository(t *testing.T) {
	t.Parallel()
	client := newGitHubReleaseClient(&http.Client{}, "")
	if _, err := client.Latest(context.Background(), "attacker/repository"); err == nil {
		t.Fatal("Latest() error = nil, want unsupported repository")
	}
}

func TestGitHubReleaseClientDownload(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "asset"), nil
	})
	client := newGitHubReleaseClient(&http.Client{Transport: transport}, "")
	var output strings.Builder
	if err := client.Download(context.Background(), "https://github.com/owner/repo/releases/download/v1/asset", &output, 10); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if output.String() != "asset" {
		t.Fatalf("Download() = %q", output.String())
	}
}

func TestGitHubReleaseClientDownloadLimit(t *testing.T) {
	t.Parallel()
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "too large"), nil
	})
	client := newGitHubReleaseClient(&http.Client{Transport: transport}, "")
	if err := client.Download(context.Background(), "https://github.com/asset", io.Discard, 3); err == nil {
		t.Fatal("Download() error = nil, want size error")
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header),
	}
}
