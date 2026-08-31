package main

import (
	"fmt"
	"strings"
)

type toolSpec struct {
	Name         string
	Binary       string
	Repository   string
	VersionArgs  []string
	ChecksumName func(version string) string
	NativeAsset  func(version string, format packageFormat, arch string) (string, bool)
	ArchiveAsset func(version, arch string) (string, bool)
}

func goreleaserSpec() toolSpec {
	return toolSpec{
		Name:         "GoReleaser",
		Binary:       "goreleaser",
		Repository:   "goreleaser/goreleaser",
		VersionArgs:  []string{"--version"},
		ChecksumName: func(string) string { return "checksums.txt" },
		NativeAsset:  goreleaserNativeAsset,
		ArchiveAsset: goreleaserArchiveAsset,
	}
}

func golangCILintSpec() toolSpec {
	return toolSpec{
		Name:        "golangci-lint",
		Binary:      "golangci-lint",
		Repository:  "golangci/golangci-lint",
		VersionArgs: []string{"version"},
		ChecksumName: func(version string) string {
			return "golangci-lint-" + version + "-checksums.txt"
		},
		NativeAsset:  golangCILintNativeAsset,
		ArchiveAsset: golangCILintArchiveAsset,
	}
}

func goreleaserNativeAsset(version string, format packageFormat, arch string) (string, bool) {
	var packageArch string
	switch format {
	case formatDeb:
		packageArch = mapArchitecture(arch, map[string]string{"amd64": "amd64", "arm64": "arm64"})
		if packageArch == "" {
			return "", false
		}
		return fmt.Sprintf("goreleaser_%s_%s.deb", version, packageArch), true
	case formatRPM:
		packageArch = mapArchitecture(arch, map[string]string{"amd64": "x86_64", "arm64": "aarch64"})
		if packageArch == "" {
			return "", false
		}
		return fmt.Sprintf("goreleaser-%s-1.%s.rpm", version, packageArch), true
	case formatAPK:
		packageArch = mapArchitecture(arch, map[string]string{"amd64": "x86_64", "arm64": "aarch64"})
		if packageArch == "" {
			return "", false
		}
		return fmt.Sprintf("goreleaser_%s_%s.apk", version, packageArch), true
	case formatArch:
		packageArch = mapArchitecture(arch, map[string]string{"amd64": "x86_64", "arm64": "aarch64"})
		if packageArch == "" {
			return "", false
		}
		return fmt.Sprintf("goreleaser-%s-1-%s.pkg.tar.zst", version, packageArch), true
	case formatArchive:
		return "", false
	default:
		return "", false
	}
}

func goreleaserArchiveAsset(_ string, arch string) (string, bool) {
	archiveArch := mapArchitecture(arch, map[string]string{"amd64": "x86_64", "arm64": "arm64"})
	if archiveArch == "" {
		return "", false
	}
	return "goreleaser_Linux_" + archiveArch + ".tar.gz", true
}

func golangCILintNativeAsset(version string, format packageFormat, arch string) (string, bool) {
	if format != formatDeb && format != formatRPM {
		return "", false
	}
	packageArch := mapArchitecture(arch, map[string]string{"amd64": "amd64", "arm64": "arm64"})
	if packageArch == "" {
		return "", false
	}
	return fmt.Sprintf("golangci-lint-%s-linux-%s.%s", version, packageArch, format), true
}

func golangCILintArchiveAsset(version, arch string) (string, bool) {
	archiveArch := mapArchitecture(arch, map[string]string{"amd64": "amd64", "arm64": "arm64"})
	if archiveArch == "" {
		return "", false
	}
	return fmt.Sprintf("golangci-lint-%s-linux-%s.tar.gz", version, archiveArch), true
}

func mapArchitecture(arch string, mapping map[string]string) string {
	return mapping[arch]
}

func parseInstalledToolVersion(output string) (string, error) {
	for field := range strings.FieldsSeq(output) {
		candidate := strings.Trim(field, "vV,;:()[]{}")
		if validToolVersion(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to parse version from %q", strings.TrimSpace(output))
}

func validToolVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for index, character := range part {
			if character >= '0' && character <= '9' {
				continue
			}
			if index > 0 && ((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '-' || character == '+') {
				continue
			}
			return false
		}
	}
	return true
}
