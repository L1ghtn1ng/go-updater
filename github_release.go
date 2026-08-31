package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	maxReleaseResponse = 4 << 20
	maxChecksumFile    = 2 << 20
	maxReleaseAsset    = 256 << 20
)

type releaseAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func (r release) asset(name string) (releaseAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

type releaseService interface {
	Latest(ctx context.Context, repository string) (release, error)
	Download(ctx context.Context, downloadURL string, output io.Writer, limit int64) error
}

type githubReleaseClient struct {
	httpClient *http.Client
	token      string
}

func newGitHubReleaseClient(httpClient *http.Client, token string) *githubReleaseClient {
	return &githubReleaseClient{httpClient: httpClient, token: strings.TrimSpace(token)}
}

func (c *githubReleaseClient) Latest(ctx context.Context, repository string) (release, error) {
	if repository != "goreleaser/goreleaser" && repository != "golangci/golangci-lint" {
		return release{}, fmt.Errorf("unsupported GitHub repository %q", repository)
	}
	apiURL := "https://api.github.com/repos/" + repository + "/releases/latest"

	var result release
	if err := c.getJSON(ctx, apiURL, &result); err != nil {
		return release{}, fmt.Errorf("fetch latest %s release: %w", repository, err)
	}
	if !validReleaseTag(result.TagName) {
		return release{}, fmt.Errorf("latest %s release returned invalid tag %q", repository, result.TagName)
	}
	if len(result.Assets) == 0 {
		return release{}, fmt.Errorf("latest %s release contains no assets", repository)
	}
	return result, nil
}

func (c *githubReleaseClient) getJSON(ctx context.Context, requestURL string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	c.setHeaders(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("GitHub API returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseResponse+1))
	if err != nil {
		return fmt.Errorf("read GitHub response: %w", err)
	}
	if len(data) > maxReleaseResponse {
		return fmt.Errorf("GitHub response exceeds %d bytes", maxReleaseResponse)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func (c *githubReleaseClient) Download(ctx context.Context, downloadURL string, output io.Writer, limit int64) error {
	parsedURL, err := url.Parse(downloadURL)
	if err != nil {
		return fmt.Errorf("parse download URL: %w", err)
	}
	if parsedURL.Scheme != "https" || parsedURL.Hostname() != "github.com" {
		return fmt.Errorf("refusing untrusted download URL %q", downloadURL)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	c.setHeaders(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", response.Status)
	}
	if response.ContentLength > limit {
		return fmt.Errorf("download is too large: %d bytes exceeds %d", response.ContentLength, limit)
	}

	limited := &io.LimitedReader{R: response.Body, N: limit + 1}
	written, err := io.Copy(output, limited)
	if err != nil {
		return err
	}
	if written > limit {
		return fmt.Errorf("download exceeds %d bytes", limit)
	}
	return nil
}

func (c *githubReleaseClient) setHeaders(request *http.Request) {
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", userAgent)
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func validReleaseTag(tag string) bool {
	version := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	return validToolVersion(version)
}
