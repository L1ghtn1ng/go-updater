package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func main() {
	var (
		versionFlag     = flag.String("version", "", "Go version to install, e.g. 'go1.26.0'. If empty, the latest version is used.")
		dryRunFlag      = flag.Bool("dry-run", false, "Only print actions without executing them.")
		noPathUpdate    = flag.Bool("no-path-update", false, "Do not modify profile files to add /usr/local/go/bin to PATH.")
		systemPathFlag  = flag.Bool("system", false, "Also add PATH entry system-wide under /etc/profile.d (requires sudo).")
		downloadDirFlag = flag.String("download-dir", "", "Directory to place the downloaded archive (defaults to system temp dir).")
	)
	flag.Parse()

	// Determine target version
	var version string
	var err error
	if strings.TrimSpace(*versionFlag) == "" {
		version, err = fetchLatestVersion()
		must(err, "fetch latest version")
	} else {
		// Sanitize and normalize a user-supplied version (handles pasted text with timestamps, etc.)
		version = cleanVersionInput(*versionFlag)
	}

	// Up-to-date check: if an existing Go installation matches the target version, exit early.
	if installed, err := getInstalledGoVersion(); err == nil && installed == version {
		log("Go is already up to date (%s). Nothing to do.", version)
		return
	}

	// Build download URL based on runtime OS/Arch
	goos, goarch, err := resolveTarget()
	must(err, "resolve target platform")

	tarName := fmt.Sprintf("%s.%s-%s.tar.gz", version, goos, goarch)
	url := "https://go.dev/dl/" + tarName

	// Decide download dir
	dlDir := *downloadDirFlag
	if dlDir == "" {
		dlDir = os.TempDir()
	}
	must(os.MkdirAll(dlDir, 0o755), "create download dir")
	tarPath := filepath.Join(dlDir, tarName)

	log("Target version: %s", version)
	log("Platform: %s/%s", goos, goarch)
	log("Download: %s\n       to: %s", url, tarPath)

	if *dryRunFlag {
		printPlan(version, goos, goarch, url, tarPath, *noPathUpdate, *systemPathFlag)
		return
	}

	// Download the archive if not already present
	if _, err := os.Stat(tarPath); err == nil {
		log("Using existing archive: %s", tarPath)
	} else {
		must(downloadFile(url, tarPath), "download archive")
		log("Downloaded: %s", tarPath)
	}

	// Remove any previous installation
	must(runAsRoot("rm", "-rf", "/usr/local/go"), "remove previous /usr/local/go")

	// Extract archive into /usr/local
	must(runAsRoot("tar", "-C", "/usr/local", "-xzf", tarPath), "extract archive to /usr/local")
	log("Extracted to /usr/local/go")

	// Ensure PATH contains /usr/local/go/bin
	if !*noPathUpdate {
		must(ensureUserPath(), "ensure user PATH in ~/.profile")
		if *systemPathFlag {
			if err := ensureSystemPath(); err != nil {
				warn("system-wide PATH update failed: %v", err)
			}
		}
		// Make the current process aware for immediate verification
		os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+"/usr/local/go/bin")
	}

	// Verify installation using an absolute path (PATH-independent)
	out, err := exec.Command("/usr/local/go/bin/go", "version").CombinedOutput()
	must(err, "verify: running '/usr/local/go/bin/go version'\nOutput: %s", string(out))
	fmt.Print(string(out))

	if !strings.Contains(string(out), version) {
		warn("Installed Go reported '%s' which does not contain expected version '%s'", strings.TrimSpace(string(out)), version)
	}

	log("Go %s installed successfully.", version)
	log("Note: You may need to start a new shell session for PATH changes to take effect, or run: 'source ~/.profile' (Linux) or 'source ~/.zprofile' (macOS)")
}

func fetchLatestVersion() (string, error) {
	const vURL = "https://go.dev/VERSION?m=text"
	client := new(http.Client{Timeout: 15 * time.Second})
	resp, err := client.Get(vURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s", resp.StatusCode, vURL)
	}
	// Only read the first line from the response; the endpoint may return multiple lines.
	reader := bufio.NewReader(io.LimitReader(resp.Body, 1024))
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	ver := strings.TrimSpace(line)
	if ver == "" || !strings.HasPrefix(ver, "go") {
		return "", fmt.Errorf("invalid version string: %q", ver)
	}
	return ver, nil
}

func resolveTarget() (string, string, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	switch goos {
	case "linux", "darwin":
	default:
		return "", "", fmt.Errorf("unsupported OS: %s (only linux/darwin supported by this installer)", goos)
	}

	// Map any special arch values to Go download naming if needed
	switch goarch {
	case "amd64", "arm64", "386":
	case "x86_64":
		goarch = "amd64"
	case "aarch64":
		goarch = "arm64"
	default:
		return "", "", fmt.Errorf("unsupported arch: %s", goarch)
	}

	return goos, goarch, nil
}

func downloadFile(url, toPath string) error {
	out, err := os.Create(toPath)
	if err != nil {
		return err
	}
	defer out.Close()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "go-updater/1.0 (https://github.com/L1ghtn1ng/go-updater)")

	client := new(http.Client{Timeout: 0})
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s -> HTTP %d", url, resp.StatusCode)
	}

	_, err = io.Copy(out, resp.Body)
	return err
}

func ensureUserPath() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	line := "export PATH=$PATH:/usr/local/go/bin"

	var candidates []string
	if runtime.GOOS == "darwin" {
		candidates = []string{".zprofile", ".zshrc", ".bash_profile", ".profile"}
	} else {
		candidates = []string{".profile"}
	}

	// If any existing file already contains the PATH, do nothing.
	for _, name := range candidates {
		path := filepath.Join(home, name)
		if data, err := os.ReadFile(path); err == nil {
			if containsProfileLine(string(data), line) {
				log("User PATH already contains /usr/local/go/bin in %s", path)
				return nil
			}
		}
	}

	// Append to the first existing file among candidates.
	for _, name := range candidates {
		path := filepath.Join(home, name)
		if _, err := os.Stat(path); err == nil {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			writer := bufio.NewWriter(f)
			fmt.Fprintln(writer)
			fmt.Fprintln(writer, "# Added by go-updater to expose Go binaries")
			fmt.Fprintln(writer, line)
			if err := writer.Flush(); err != nil {
				return err
			}
			log("Added PATH update to %s", path)
			return nil
		}
	}

	// Otherwise create the first candidate and write to it.
	target := filepath.Join(home, candidates[0])
	f, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	writer := bufio.NewWriter(f)
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "# Added by go-updater to expose Go binaries")
	fmt.Fprintln(writer, line)
	if err := writer.Flush(); err != nil {
		return err
	}
	log("Added PATH update to %s", target)
	return nil
}

func containsProfileLine(content, target string) bool {
	// consider whitespace variations
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == target {
			return true
		}
		// allow forms like: export PATH=/usr/local/go/bin:$PATH or similar
		if strings.Contains(line, "/usr/local/go/bin") && strings.Contains(line, "export PATH") {
			return true
		}
	}
	return false
}

func ensureSystemPath() error {
	if runtime.GOOS == "darwin" {
		// Prefer /etc/paths.d on macOS
		content := "/usr/local/go/bin\n"

		tmp, err := os.CreateTemp("", "golang-path-*.txt")
		if err != nil {
			return err
		}
		tmpPath := tmp.Name()
		if _, err := tmp.WriteString(content); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()

		// Try /etc/paths.d first
		if err := runAsRoot("install", "-m", "0644", tmpPath, "/etc/paths.d/go"); err == nil {
			os.Remove(tmpPath)
			log("Added system PATH at /etc/paths.d/go")
			return nil
		}

		// Fallback: append to /etc/zprofile
		cmd := fmt.Sprintf("printf '%s' >> /etc/zprofile", strings.ReplaceAll("export PATH=\"$PATH:/usr/local/go/bin\"\n", "'", "'\\''"))
		if err := runAsRoot("sh", "-c", cmd); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to update /etc/paths.d or /etc/zprofile: %w", err)
		}
		os.Remove(tmpPath)
		log("Appended system PATH to /etc/zprofile")
		return nil
	}

	// Linux and others: use /etc/profile.d
	content := "# /etc/profile.d/golang-path.sh\n# Added by go-updater\nexport PATH=\"$PATH:/usr/local/go/bin\"\n"

	tmp, err := os.CreateTemp("", "golang-path-*.sh")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	// Try /etc/profile.d first
	if err := runAsRoot("install", "-m", "0644", tmpPath, "/etc/profile.d/golang-path.sh"); err == nil {
		os.Remove(tmpPath)
		log("Added system PATH at /etc/profile.d/golang-path.sh")
		return nil
	}

	// Fallback: append to /etc/profile
	cmd := fmt.Sprintf("printf '%s' >> /etc/profile", strings.ReplaceAll(content, "'", "'\\''"))
	if err := runAsRoot("sh", "-c", cmd); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to update /etc/profile.d or /etc/profile: %w", err)
	}
	os.Remove(tmpPath)
	log("Appended system PATH to /etc/profile")
	return nil
}

func runAsRoot(cmd string, args ...string) error {
	if isRoot() {
		execCmd := exec.Command(cmd, args...)
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		return execCmd.Run()
	}

	if _, err := exec.LookPath("sudo"); err != nil {
		return errors.New("this action requires root; please re-run with sudo")
	}
	// Try to refresh the sudo timestamp to avoid multiple prompts
	_ = exec.Command("sudo", "-v").Run()

	argv := append([]string{cmd}, args...)
	execCmd := exec.Command("sudo", argv...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin
	return execCmd.Run()
}

func isRoot() bool {
	return os.Geteuid() == 0
}

func printPlan(version, goos, goarch, url, tarPath string, noPath, system bool) {
	fmt.Println("Plan (dry-run):")
	fmt.Printf("- Determine version: %s\n", version)
	fmt.Printf("- Download %s -> %s\n", url, tarPath)
	fmt.Println("- Remove any previous /usr/local/go")
	fmt.Printf("- Extract archive into /usr/local\n")
	if !noPath {
		fmt.Println("- Add '/usr/local/go/bin' to PATH in your shell profile (idempotent)")
		if system {
			if goos == "darwin" {
				fmt.Println("- Also add system-wide PATH via /etc/paths.d (requires sudo)")
			} else {
				fmt.Println("- Also add system-wide PATH via /etc/profile.d (requires sudo)")
			}
		}
	} else {
		fmt.Println("- Skip PATH update (per --no-path-update)")
	}
	fmt.Println("- Verify with '/usr/local/go/bin/go version'")
}

func log(format string, args ...any) {
	fmt.Printf("[go-updater] "+format+"\n", args...)
}

func warn(format string, args ...any) {
	fmt.Printf("[go-updater][WARN] "+format+"\n", args...)
}

func must(err error, contextFmt string, args ...any) {
	if err == nil {
		return
	}
	msg := contextFmt
	if len(args) > 0 {
		msg = fmt.Sprintf(contextFmt, args...)
	}
	fmt.Fprintf(os.Stderr, "[go-updater][ERROR] %s: %v\n", msg, err)
	os.Exit(1)
}

// cleanVersionInput normalizes user-provided version strings. It takes only the
// first whitespace-separated token, ensures the 'go' prefix is present, and
// strips any trailing characters that are not valid in go.dev archive names.
func cleanVersionInput(versionInput string) string {
	versionInput = strings.TrimSpace(versionInput)
	if versionInput == "" {
		return versionInput
	}
	// Take the first token (users may paste: "go1.26.0 time 2025-08-27T15:49:40Z")
	fields := strings.Fields(versionInput)
	if len(fields) > 0 {
		versionInput = fields[0]
	}
	// Ensure prefix
	if !strings.HasPrefix(versionInput, "go") {
		versionInput = "go" + versionInput
	}
	// Keep only allowed characters after 'go': digits, lowercase letters (rc/beta), and dots
	bytesVersion := []byte(versionInput)
	out := make([]byte, 0, len(bytesVersion))
	for idx := range bytesVersion {
		charByte := bytesVersion[idx]
		if idx < 2 { // always keep the 'g' and 'o'
			out = append(out, charByte)
			continue
		}
		if (charByte >= '0' && charByte <= '9') || (charByte >= 'a' && charByte <= 'z') || charByte == '.' {
			out = append(out, charByte)
		} else {
			break
		}
	}
	return string(out)
}

// getInstalledGoVersion tries to detect the currently installed Go version.
// It prefers /usr/local/go/bin/go (managed by this installer) and falls back
// to any 'go' found in PATH. It returns a version string like 'go1.26.0'.
func getInstalledGoVersion() (string, error) {
	// Prefer the standard installation location first
	const stdGo = "/usr/local/go/bin/go"
	if fi, err := os.Stat(stdGo); err == nil && !fi.IsDir() {
		out, err := exec.Command(stdGo, "version").CombinedOutput()
		if err == nil {
			if v, perr := parseGoVersionOutput(string(out)); perr == nil {
				return v, nil
			}
		}
	}

	// Fallback to whatever 'go' is on PATH
	if path, err := exec.LookPath("go"); err == nil {
		out, err := exec.Command(path, "version").CombinedOutput()
		if err == nil {
			if v, perr := parseGoVersionOutput(string(out)); perr == nil {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("no installed Go found")
}

// parseGoVersionOutput extracts the 'goX.Y[.Z...]' token from `go version` output.
func parseGoVersionOutput(output string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(output))
	// Typical: "go version go1.22.6 linux/amd64"
	if len(fields) >= 3 && strings.HasPrefix(fields[2], "go") {
		return cleanVersionInput(fields[2]), nil
	}
	// Fallback: scan for any token beginning with 'go'
	for _, token := range fields {
		if strings.HasPrefix(token, "go") {
			versionToken := cleanVersionInput(token)
			if strings.HasPrefix(versionToken, "go") && len(versionToken) > 2 {
				return versionToken, nil
			}
		}
	}
	return "", fmt.Errorf("unable to parse version from: %q", output)
}
