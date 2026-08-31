package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type commandRunner interface {
	LookPath(file string) (string, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	Run(ctx context.Context, root bool, name string, args ...string) error
}

type execRunner struct {
	stdout io.Writer
	stderr io.Writer
}

func newExecRunner(stdout, stderr io.Writer) *execRunner {
	return &execRunner{stdout: stdout, stderr: stderr}
}

func (r *execRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (r *execRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // Allowlisted command without a shell.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run %s: %w: %s", name, err, output)
	}
	return output, nil
}

func (r *execRunner) Run(ctx context.Context, root bool, name string, args ...string) error {
	command, commandArgs := name, args
	if root && os.Geteuid() != 0 {
		if _, err := exec.LookPath("sudo"); err != nil {
			return errors.New("this action requires root; install sudo or run go-updater as root")
		}
		command = "sudo"
		commandArgs = append([]string{name}, args...)
	}

	cmd := exec.CommandContext(ctx, command, commandArgs...) //nolint:gosec // Allowlisted command without a shell.
	cmd.Stdout = r.stdout
	cmd.Stderr = r.stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}
