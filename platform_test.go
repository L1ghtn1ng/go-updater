package main

import (
	"strings"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	t.Parallel()
	values, err := parseOSRelease(strings.NewReader("# comment\nID=kali\nID_LIKE=\"debian ubuntu\"\nNAME='Kali Linux'\n"))
	if err != nil {
		t.Fatalf("parseOSRelease() error = %v", err)
	}
	if values["ID"] != "kali" || values["ID_LIKE"] != "debian ubuntu" || values["NAME"] != "Kali Linux" {
		t.Fatalf("parseOSRelease() = %#v", values)
	}
}

func TestDistroPackageFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		id     string
		idLike []string
		want   packageFormat
	}{
		{name: "Kali", id: "kali", idLike: []string{"debian"}, want: formatDeb},
		{name: "UbuntuLike", id: "pop", idLike: []string{"ubuntu", "debian"}, want: formatDeb},
		{name: "Fedora", id: "fedora", want: formatRPM},
		{name: "SUSELike", id: "custom", idLike: []string{"suse"}, want: formatRPM},
		{name: "Alpine", id: "alpine", want: formatAPK},
		{name: "Manjaro", id: "manjaro", idLike: []string{"arch"}, want: formatArch},
		{name: "Unknown", id: "unknown", want: formatArchive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := distroPackageFormat(test.id, test.idLike); got != test.want {
				t.Fatalf("distroPackageFormat(%q, %v) = %q, want %q", test.id, test.idLike, got, test.want)
			}
		})
	}
}

func TestDetectLinuxPlatform(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/os-release"
	writeTestFile(t, path, []byte("ID=kali\nID_LIKE=debian\n"), 0o600)
	platform, err := detectLinuxPlatform(path, "linux", "amd64")
	if err != nil {
		t.Fatalf("detectLinuxPlatform() error = %v", err)
	}
	if platform.DistroID != "kali" || platform.Arch != "amd64" || platform.Format != formatDeb {
		t.Fatalf("detectLinuxPlatform() = %#v", platform)
	}
}

func TestDetectLinuxPlatformRejectsOtherOS(t *testing.T) {
	t.Parallel()
	if _, err := detectLinuxPlatform("unused", "darwin", "arm64"); err == nil {
		t.Fatal("detectLinuxPlatform() error = nil, want unsupported OS error")
	}
}
