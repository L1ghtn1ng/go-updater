package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const userAgent = "go-updater/2 (https://github.com/L1ghtn1ng/go-updater)"

type application struct {
	stdout        io.Writer
	stderr        io.Writer
	httpClient    *http.Client
	releases      releaseService
	runner        commandRunner
	goos          string
	goarch        string
	osReleasePath string
	userHomeDir   func() (string, error)
	tempDir       func() string
}

type goOptions struct {
	version      string
	dryRun       bool
	noPathUpdate bool
	systemPath   bool
	downloadDir  string
}

type toolOptions struct {
	dryRun      bool
	downloadDir string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := newApplication(os.Stdout, os.Stderr)
	if err := app.run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "[go-updater][ERROR] %v\n", err)
		os.Exit(1)
	}
}

func newApplication(stdout, stderr io.Writer) *application {
	httpClient := &http.Client{Timeout: 10 * time.Minute}
	return &application{
		stdout:        stdout,
		stderr:        stderr,
		httpClient:    httpClient,
		releases:      newGitHubReleaseClient(httpClient, os.Getenv("GITHUB_TOKEN")),
		runner:        newExecRunner(stdout, stderr),
		goos:          runtime.GOOS,
		goarch:        runtime.GOARCH,
		osReleasePath: "/etc/os-release",
		userHomeDir:   os.UserHomeDir,
		tempDir:       os.TempDir,
	}
}

func (a *application) run(ctx context.Context, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		opts, err := parseGoOptions("go-updater", args, a.stderr)
		if err != nil {
			return ignoreHelp(err)
		}
		return a.installGo(ctx, opts)
	}

	command, commandArgs := args[0], args[1:]
	switch command {
	case "go":
		opts, err := parseGoOptions("go-updater go", commandArgs, a.stderr)
		if err != nil {
			return ignoreHelp(err)
		}
		return a.installGo(ctx, opts)
	case "goreleaser":
		return a.runToolCommand(ctx, goreleaserSpec(), command, commandArgs)
	case "golangci-lint":
		return a.runToolCommand(ctx, golangCILintSpec(), command, commandArgs)
	case "all":
		opts, err := parseAllOptions(commandArgs, a.stderr)
		if err != nil {
			return ignoreHelp(err)
		}
		if err := a.installGo(ctx, opts); err != nil {
			return err
		}
		toolOpts := toolOptions{dryRun: opts.dryRun, downloadDir: opts.downloadDir}
		if err := a.installTool(ctx, goreleaserSpec(), toolOpts); err != nil {
			return err
		}
		return a.installTool(ctx, golangCILintSpec(), toolOpts)
	case "help":
		printUsage(a.stdout)
		return nil
	default:
		printUsage(a.stderr)
		return fmt.Errorf("unknown command %q", command)
	}
}

func (a *application) runToolCommand(ctx context.Context, spec toolSpec, command string, args []string) error {
	opts, err := parseToolOptions("go-updater "+command, args, a.stderr)
	if err != nil {
		return ignoreHelp(err)
	}
	return a.installTool(ctx, spec, opts)
}

func parseGoOptions(name string, args []string, output io.Writer) (goOptions, error) {
	var opts goOptions
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.version, "version", "", "Go version to install, for example go1.27.1; latest when omitted")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "print actions without executing them")
	flags.BoolVar(&opts.noPathUpdate, "no-path-update", false, "do not add /usr/local/go/bin to shell profiles")
	flags.BoolVar(&opts.systemPath, "system", false, "also install a system-wide Go PATH entry")
	flags.StringVar(&opts.downloadDir, "download-dir", "", "directory for downloaded archives")
	if err := flags.Parse(args); err != nil {
		return goOptions{}, err
	}
	if flags.NArg() != 0 {
		return goOptions{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return opts, nil
}

func parseToolOptions(name string, args []string, output io.Writer) (toolOptions, error) {
	var opts toolOptions
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&opts.dryRun, "dry-run", false, "resolve and print actions without downloading or installing")
	flags.StringVar(&opts.downloadDir, "download-dir", "", "directory for downloaded release assets")
	if err := flags.Parse(args); err != nil {
		return toolOptions{}, err
	}
	if flags.NArg() != 0 {
		return toolOptions{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return opts, nil
}

func parseAllOptions(args []string, output io.Writer) (goOptions, error) {
	var opts goOptions
	flags := flag.NewFlagSet("go-updater all", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&opts.dryRun, "dry-run", false, "resolve and print actions without downloading or installing")
	flags.BoolVar(&opts.noPathUpdate, "no-path-update", false, "do not add /usr/local/go/bin to shell profiles")
	flags.BoolVar(&opts.systemPath, "system", false, "also install a system-wide Go PATH entry")
	flags.StringVar(&opts.downloadDir, "download-dir", "", "directory for downloaded release assets")
	if err := flags.Parse(args); err != nil {
		return goOptions{}, err
	}
	if flags.NArg() != 0 {
		return goOptions{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return opts, nil
}

func ignoreHelp(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func printUsage(output io.Writer) {
	writeBestEffort(output, "Usage:\n")
	writeBestEffort(output, "  go-updater [go] [--version goX.Y.Z] [--dry-run] [--download-dir DIR] [--no-path-update] [--system]\n")
	writeBestEffort(output, "  go-updater goreleaser [--dry-run] [--download-dir DIR]\n")
	writeBestEffort(output, "  go-updater golangci-lint [--dry-run] [--download-dir DIR]\n")
	writeBestEffort(output, "  go-updater all [--dry-run] [--download-dir DIR] [--no-path-update] [--system]\n")
}

func (a *application) log(format string, args ...any) {
	writeBestEffort(a.stdout, "[go-updater] "+format+"\n", args...)
}

func (a *application) warn(format string, args ...any) {
	writeBestEffort(a.stderr, "[go-updater][WARN] "+format+"\n", args...)
}

func writeBestEffort(output io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(output, format, args...)
}
