package main

import "testing"

func TestToolAssetNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		choose func() (string, bool)
		want   string
		wantOK bool
	}{
		{name: "GoReleaserDebAMD64", choose: func() (string, bool) { return goreleaserNativeAsset("2.18.0", formatDeb, "amd64") }, want: "goreleaser_2.18.0_amd64.deb", wantOK: true},
		{name: "GoReleaserRPMArm64", choose: func() (string, bool) { return goreleaserNativeAsset("2.18.0", formatRPM, "arm64") }, want: "goreleaser-2.18.0-1.aarch64.rpm", wantOK: true},
		{name: "GoReleaserArchAMD64", choose: func() (string, bool) { return goreleaserNativeAsset("2.18.0", formatArch, "amd64") }, want: "goreleaser-2.18.0-1-x86_64.pkg.tar.zst", wantOK: true},
		{name: "GoReleaserArchive", choose: func() (string, bool) { return goreleaserArchiveAsset("2.18.0", "amd64") }, want: "goreleaser_Linux_x86_64.tar.gz", wantOK: true},
		{name: "LintDebArm64", choose: func() (string, bool) { return golangCILintNativeAsset("2.13.2", formatDeb, "arm64") }, want: "golangci-lint-2.13.2-linux-arm64.deb", wantOK: true},
		{name: "LintNoAPK", choose: func() (string, bool) { return golangCILintNativeAsset("2.13.2", formatAPK, "amd64") }, wantOK: false},
		{name: "LintArchive", choose: func() (string, bool) { return golangCILintArchiveAsset("2.13.2", "amd64") }, want: "golangci-lint-2.13.2-linux-amd64.tar.gz", wantOK: true},
		{name: "UnsupportedArchitecture", choose: func() (string, bool) { return goreleaserArchiveAsset("2.18.0", "s390x") }, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := test.choose()
			if got != test.want || ok != test.wantOK {
				t.Fatalf("asset = %q, %v; want %q, %v", got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestToolAssetSelectorsReject32Bit(t *testing.T) {
	t.Parallel()
	selectors := map[string]func() (string, bool){
		"GoReleaserDeb":     func() (string, bool) { return goreleaserNativeAsset("2.18.0", formatDeb, "386") },
		"GoReleaserRPM":     func() (string, bool) { return goreleaserNativeAsset("2.18.0", formatRPM, "386") },
		"GoReleaserAPK":     func() (string, bool) { return goreleaserNativeAsset("2.18.0", formatAPK, "386") },
		"GoReleaserArch":    func() (string, bool) { return goreleaserNativeAsset("2.18.0", formatArch, "386") },
		"GoReleaserArchive": func() (string, bool) { return goreleaserArchiveAsset("2.18.0", "386") },
		"LintDeb":           func() (string, bool) { return golangCILintNativeAsset("2.13.2", formatDeb, "386") },
		"LintRPM":           func() (string, bool) { return golangCILintNativeAsset("2.13.2", formatRPM, "386") },
		"LintArchive":       func() (string, bool) { return golangCILintArchiveAsset("2.13.2", "386") },
	}
	for name, selectAsset := range selectors {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if asset, supported := selectAsset(); supported || asset != "" {
				t.Fatalf("32-bit asset = %q, supported=%v; want rejected", asset, supported)
			}
		})
	}
}

func TestParseInstalledToolVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		output string
		want   string
	}{
		{output: "goreleaser version 2.18.0\n", want: "2.18.0"},
		{output: "golangci-lint has version 2.13.2 built with go1.27.0", want: "2.13.2"},
		{output: "tool v3.4.5-rc1", want: "3.4.5-rc1"},
	}
	for _, test := range tests {
		if got, err := parseInstalledToolVersion(test.output); err != nil || got != test.want {
			t.Fatalf("parseInstalledToolVersion(%q) = %q, %v; want %q", test.output, got, err, test.want)
		}
	}
}
