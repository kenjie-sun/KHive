//go:build ignore

// Run selected KHive tests in a new network namespace as uid/gid 65534.
// It cannot fall back to the host network or to root execution.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: linux-unit-runner test-binary [test flags]")
	}
	if os.Args[1] != "--inside" {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()
		args := append([]string{"--inside"}, os.Args[1:]...)
		cmd := exec.CommandContext(ctx, "/proc/self/exe", args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET, Pdeathsig: syscall.SIGKILL}
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	if len(os.Args) < 3 {
		return fmt.Errorf("test binary is required")
	}
	if err := exec.Command("ip", "link", "set", "lo", "up").Run(); err != nil {
		return fmt.Errorf("isolated loopback setup: %w", err)
	}
	dir, err := os.MkdirTemp("/tmp", "khive-unit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.Chown(dir, 65534, 65534); err != nil {
		return err
	}
	bin, err := filepath.Abs(os.Args[2])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, os.Args[3:]...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "TMPDIR=" + dir, "HOME=" + dir}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}, Pdeathsig: syscall.SIGKILL}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
