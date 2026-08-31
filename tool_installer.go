package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type selectedAsset struct {
	Asset  releaseAsset
	Format packageFormat
}

func (a *application) installTool(ctx context.Context, spec toolSpec, opts toolOptions) error {
	latest, err := a.releases.Latest(ctx, spec.Repository)
	if err != nil {
		return err
	}
	version := strings.TrimPrefix(latest.TagName, "v")

	installed, installedPath, err := a.installedToolVersion(ctx, spec, version)
	if err == nil && installed == version {
		a.warnIfToolIsShadowed(spec, installedPath)
		a.log("%s is already up to date (%s). Nothing to do.", spec.Name, version)
		return nil
	}
	if err != nil {
		a.log("%s is not installed or its version could not be determined; installing %s.", spec.Name, version)
	}

	platform, err := detectLinuxPlatform(a.osReleasePath, a.goos, a.goarch)
	if err != nil {
		return fmt.Errorf("resolve %s target: %w", spec.Name, err)
	}
	selection, err := a.selectToolAsset(spec, version, latest, platform)
	if err != nil {
		return err
	}
	checksumAsset, found := latest.asset(spec.ChecksumName(version))
	if !found {
		return fmt.Errorf("%s release %s does not contain checksum asset %q", spec.Name, latest.TagName, spec.ChecksumName(version))
	}

	a.log("Target: %s %s", spec.Name, version)
	a.log("Platform: %s/%s (%s, %s)", a.goos, a.goarch, platform.DistroID, selection.Format)
	a.log("Asset: %s", selection.Asset.Name)
	if opts.dryRun {
		a.printToolPlan(spec, version, selection)
		return nil
	}

	downloadDir, cleanup, err := a.prepareDownloadDir(opts.downloadDir)
	if err != nil {
		return err
	}
	defer cleanup()

	assetPath, err := a.downloadVerifiedAsset(ctx, selection.Asset, checksumAsset, downloadDir)
	if err != nil {
		return fmt.Errorf("download %s: %w", spec.Name, err)
	}
	if selection.Format == formatArchive {
		if err := a.installArchiveBinary(ctx, spec, assetPath); err != nil {
			return err
		}
	} else if err := a.installNativePackage(ctx, selection.Format, assetPath); err != nil {
		return err
	}

	installed, installedPath, err = a.installedToolVersion(ctx, spec, version)
	if err != nil {
		return fmt.Errorf("verify %s installation: %w", spec.Name, err)
	}
	if installed != version {
		return fmt.Errorf("verify %s installation: expected %s, got %s", spec.Name, version, installed)
	}
	a.warnIfToolIsShadowed(spec, installedPath)
	a.log("%s %s installed successfully.", spec.Name, version)
	return nil
}

func (a *application) selectToolAsset(spec toolSpec, version string, latest release, platform linuxPlatform) (selectedAsset, error) {
	format := platform.Format
	if nativeName, supported := spec.NativeAsset(version, format, platform.Arch); supported {
		if _, err := packageInstaller(format); err == nil {
			if _, err := a.runner.LookPath(packageInstallerName(format)); err == nil {
				if asset, found := latest.asset(nativeName); found {
					return selectedAsset{Asset: asset, Format: format}, nil
				}
			}
		}
	}

	archiveName, supported := spec.ArchiveAsset(version, platform.Arch)
	if !supported {
		return selectedAsset{}, fmt.Errorf("%s does not publish a supported Linux asset for architecture %s", spec.Name, platform.Arch)
	}
	asset, found := latest.asset(archiveName)
	if !found {
		return selectedAsset{}, fmt.Errorf("%s release v%s contains neither a usable native package nor archive %q", spec.Name, version, archiveName)
	}
	return selectedAsset{Asset: asset, Format: formatArchive}, nil
}

func packageInstaller(format packageFormat) ([]string, error) {
	switch format {
	case formatDeb:
		return []string{"dpkg", "--install"}, nil
	case formatRPM:
		return []string{"rpm", "--upgrade", "--replacepkgs"}, nil
	case formatAPK:
		return []string{"apk", "add", "--allow-untrusted"}, nil
	case formatArch:
		return []string{"pacman", "-U", "--noconfirm"}, nil
	case formatArchive:
		return nil, fmt.Errorf("archive installation does not use a package manager")
	default:
		return nil, fmt.Errorf("unsupported package format %q", format)
	}
}

func packageInstallerName(format packageFormat) string {
	command, err := packageInstaller(format)
	if err != nil {
		return ""
	}
	return command[0]
}

func (a *application) installedToolVersion(ctx context.Context, spec toolSpec, preferred string) (string, string, error) {
	paths := []string{filepath.Join("/usr/local/bin", spec.Binary), filepath.Join("/usr/bin", spec.Binary)}
	if path, err := a.runner.LookPath(spec.Binary); err == nil {
		paths = append([]string{path}, paths...)
	}

	seen := make(map[string]struct{}, len(paths))
	var lastErr error
	var firstVersion, firstPath string
	for _, path := range paths {
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		output, err := a.runner.Output(ctx, path, spec.VersionArgs...)
		if err != nil {
			lastErr = err
			continue
		}
		version, err := parseInstalledToolVersion(string(output))
		if err != nil {
			lastErr = err
			continue
		}
		if version == preferred {
			return version, path, nil
		}
		if firstVersion == "" {
			firstVersion, firstPath = version, path
		}
	}
	if firstVersion != "" {
		return firstVersion, firstPath, nil
	}
	if lastErr != nil {
		return "", "", lastErr
	}
	return "", "", fmt.Errorf("%s executable not found", spec.Binary)
}

func (a *application) warnIfToolIsShadowed(spec toolSpec, installedPath string) {
	resolvedPath, err := a.runner.LookPath(spec.Binary)
	if err == nil && resolvedPath != installedPath {
		a.warn("%s %s is current, but PATH resolves %s; remove or reorder the shadowing executable", spec.Name, installedPath, resolvedPath)
	}
}

func (a *application) prepareDownloadDir(requested string) (string, func(), error) {
	if requested != "" {
		if err := os.MkdirAll(requested, 0o750); err != nil {
			return "", func() {}, fmt.Errorf("create download directory: %w", err)
		}
		return requested, func() {}, nil
	}

	directory, err := os.MkdirTemp(a.tempDir(), "go-updater-download-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary download directory: %w", err)
	}
	return directory, func() {
		if err := os.RemoveAll(directory); err != nil {
			a.warn("remove temporary download directory %s: %v", directory, err)
		}
	}, nil
}

func (a *application) downloadVerifiedAsset(ctx context.Context, asset, checksumAsset releaseAsset, directory string) (string, error) {
	var checksumData bytes.Buffer
	if err := a.releases.Download(ctx, checksumAsset.DownloadURL, &checksumData, maxChecksumFile); err != nil {
		return "", fmt.Errorf("download checksum manifest: %w", err)
	}
	expectedHash, err := checksumForAsset(checksumData.String(), asset.Name)
	if err != nil {
		return "", err
	}

	assetPath := filepath.Join(directory, asset.Name)
	if matches, err := fileMatchesSHA256(assetPath, expectedHash); err == nil && matches {
		a.log("Using verified cached asset: %s", assetPath)
		return assetPath, nil
	}

	partial, err := os.CreateTemp(directory, "."+asset.Name+".partial-*")
	if err != nil {
		return "", fmt.Errorf("create partial download: %w", err)
	}
	partialPath := partial.Name()
	cleanupPartial := func() {
		if err := os.Remove(partialPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			a.warn("remove partial download %s: %v", partialPath, err)
		}
	}
	defer cleanupPartial()

	if err := a.releases.Download(ctx, asset.DownloadURL, partial, maxReleaseAsset); err != nil {
		_ = partial.Close()
		return "", err
	}
	if err := partial.Sync(); err != nil {
		_ = partial.Close()
		return "", fmt.Errorf("sync partial download: %w", err)
	}
	if err := partial.Close(); err != nil {
		return "", fmt.Errorf("close partial download: %w", err)
	}
	matches, err := fileMatchesSHA256(partialPath, expectedHash)
	if err != nil {
		return "", err
	}
	if !matches {
		return "", fmt.Errorf("SHA-256 mismatch for %s", asset.Name)
	}
	if err := os.Rename(partialPath, assetPath); err != nil {
		return "", fmt.Errorf("publish downloaded asset: %w", err)
	}
	a.log("Downloaded and verified: %s", assetPath)
	return assetPath, nil
}

func checksumForAsset(manifest, assetName string) ([]byte, error) {
	for line := range strings.SplitSeq(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != assetName {
			continue
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("invalid SHA-256 for %s", assetName)
		}
		return digest, nil
	}
	return nil, fmt.Errorf("checksum manifest does not contain %s", assetName)
}

func fileMatchesSHA256(path string, expected []byte) (bool, error) {
	file, err := os.Open(path) //nolint:gosec // Exact release asset path.
	if err != nil {
		return false, err
	}
	defer func() {
		_ = file.Close()
	}()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, fmt.Errorf("hash %s: %w", path, err)
	}
	return bytes.Equal(hash.Sum(nil), expected), nil
}

func (a *application) installNativePackage(ctx context.Context, format packageFormat, path string) error {
	command, err := packageInstaller(format)
	if err != nil {
		return err
	}
	args := append(command[1:], path)
	if err := a.runner.Run(ctx, true, command[0], args...); err != nil {
		return fmt.Errorf("install %s package: %w", format, err)
	}
	return nil
}

func (a *application) installArchiveBinary(ctx context.Context, spec toolSpec, archivePath string) error {
	directory, err := os.MkdirTemp(a.tempDir(), "go-updater-extract-*")
	if err != nil {
		return fmt.Errorf("create extraction directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			a.warn("remove extraction directory %s: %v", directory, err)
		}
	}()

	binaryPath := filepath.Join(directory, spec.Binary)
	if err := extractBinary(archivePath, spec.Binary, binaryPath); err != nil {
		return fmt.Errorf("extract %s: %w", spec.Name, err)
	}
	if err := a.runner.Run(ctx, true, "install", "-d", "-m", "0755", "/usr/local/bin"); err != nil {
		return fmt.Errorf("create /usr/local/bin: %w", err)
	}
	if err := a.runner.Run(ctx, true, "install", "-m", "0755", binaryPath, filepath.Join("/usr/local/bin", spec.Binary)); err != nil {
		return fmt.Errorf("install %s binary: %w", spec.Name, err)
	}
	return nil
}

func extractBinary(archivePath, binaryName, destination string) error {
	file, err := os.Open(archivePath) //nolint:gosec // Verified release asset.
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() {
		_ = gzipReader.Close()
	}()

	tarReader := tar.NewReader(gzipReader)
	found := false
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		cleanName := filepath.Clean(header.Name)
		if cleanName == "." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanName) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		if filepath.Base(cleanName) != binaryName {
			continue
		}
		if found {
			return fmt.Errorf("archive contains multiple %s entries", binaryName)
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxReleaseAsset {
			return fmt.Errorf("archive entry %q is not a safe regular file", header.Name)
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700) //nolint:gosec // Private temporary path.
		if err != nil {
			return err
		}
		written, copyErr := io.CopyN(output, tarReader, header.Size)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != header.Size {
			return fmt.Errorf("short archive entry: wrote %d of %d bytes", written, header.Size)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("archive does not contain %s", binaryName)
	}
	return nil
}

func (a *application) printToolPlan(spec toolSpec, version string, selection selectedAsset) {
	writeBestEffort(a.stdout, "Plan (dry-run):\n")
	writeBestEffort(a.stdout, "- Install %s %s from %s\n", spec.Name, version, selection.Asset.DownloadURL)
	writeBestEffort(a.stdout, "- Verify the release asset against the publisher SHA-256 manifest\n")
	if selection.Format == formatArchive {
		writeBestEffort(a.stdout, "- Install the verified %s binary into /usr/local/bin\n", spec.Binary)
	} else {
		writeBestEffort(a.stdout, "- Install the verified %s package with the system package database\n", selection.Format)
	}
	writeBestEffort(a.stdout, "- Verify with '%s %s'\n", spec.Binary, strings.Join(spec.VersionArgs, " "))
}
