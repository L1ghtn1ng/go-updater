package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type packageFormat string

const (
	formatArchive packageFormat = "archive"
	formatDeb     packageFormat = "deb"
	formatRPM     packageFormat = "rpm"
	formatAPK     packageFormat = "apk"
	formatArch    packageFormat = "arch"
)

type linuxPlatform struct {
	DistroID   string
	DistroLike []string
	Arch       string
	Format     packageFormat
}

func detectLinuxPlatform(path, goos, goarch string) (linuxPlatform, error) {
	if goos != "linux" {
		return linuxPlatform{}, fmt.Errorf("unsupported OS %q: GoReleaser and golangci-lint installation is Linux-only", goos)
	}

	file, err := os.Open(path) //nolint:gosec // Fixed application path.
	if err != nil {
		return linuxPlatform{}, fmt.Errorf("open os-release: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	values, err := parseOSRelease(io.LimitReader(file, 64<<10))
	if err != nil {
		return linuxPlatform{}, err
	}
	distroID := strings.ToLower(values["ID"])
	distroLike := strings.Fields(strings.ToLower(values["ID_LIKE"]))
	if distroID == "" {
		return linuxPlatform{}, fmt.Errorf("os-release does not define ID")
	}

	return linuxPlatform{
		DistroID:   distroID,
		DistroLike: distroLike,
		Arch:       goarch,
		Format:     distroPackageFormat(distroID, distroLike),
	}, nil
}

func parseOSRelease(reader io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			if value[0] == '"' {
				unquoted, err := strconv.Unquote(value)
				if err != nil {
					return nil, fmt.Errorf("parse os-release %s: %w", key, err)
				}
				value = unquoted
			} else {
				value = value[1 : len(value)-1]
			}
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read os-release: %w", err)
	}
	return values, nil
}

func distroPackageFormat(id string, idLike []string) packageFormat {
	identifiers := append([]string{id}, idLike...)
	for _, identifier := range identifiers {
		switch identifier {
		case "debian", "ubuntu", "kali", "linuxmint", "raspbian":
			return formatDeb
		case "rhel", "fedora", "centos", "rocky", "almalinux", "suse", "opensuse", "opensuse-leap", "opensuse-tumbleweed":
			return formatRPM
		case "alpine":
			return formatAPK
		case "arch", "manjaro":
			return formatArch
		}
	}
	return formatArchive
}
