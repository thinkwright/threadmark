package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tmjournal "github.com/thinkwright/threadmark/internal/journal"
)

const daemonPIDFile = "daemon.pid"

func daemonCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: threadmark daemon <start|stop|restart|status> [options]")
		return 2
	}

	switch args[0] {
	case "start":
		return daemonStart(args[1:], stdout, stderr)
	case "stop":
		return daemonStop(args[1:], stdout, stderr)
	case "restart":
		return daemonRestart(args[1:], stdout, stderr)
	case "status":
		return daemonStatusCommand(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "threadmark: unknown daemon command %q\n", args[0])
		return 2
	}
}

func daemonStart(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	rootFlag := flags.String("root", "", "threadmark root directory (default ~/.threadmark)")
	socketFlag := flags.String("socket", "", "unix socket path (default <root>/daemon.sock)")
	daemonFlag := flags.String("daemon-command", "", "threadmarkd command path (default sibling binary or PATH)")
	timeout := flags.Duration("timeout", 2*time.Second, "startup timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	root, socketPath, err := daemonPaths(*rootFlag, *socketFlag)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark daemon start: %v\n", err)
		return 1
	}
	if socketAvailable(socketPath) {
		fmt.Fprintf(stdout, "threadmarkd already running on %s\n", socketPath)
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := ensureDaemon(ctx, root, socketPath, *daemonFlag); err != nil {
		fmt.Fprintf(stderr, "threadmark daemon start: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "threadmarkd started on %s\n", socketPath)
	return 0
}

func daemonStop(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	flags.SetOutput(stderr)
	rootFlag := flags.String("root", "", "threadmark root directory (default ~/.threadmark)")
	socketFlag := flags.String("socket", "", "unix socket path (default <root>/daemon.sock)")
	timeout := flags.Duration("timeout", 2*time.Second, "shutdown timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	root, socketPath, err := daemonPaths(*rootFlag, *socketFlag)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark daemon stop: %v\n", err)
		return 1
	}
	if err := stopDaemon(root, socketPath, *timeout); err != nil {
		fmt.Fprintf(stderr, "threadmark daemon stop: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "threadmarkd stopped")
	return 0
}

func daemonRestart(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("daemon restart", flag.ContinueOnError)
	flags.SetOutput(stderr)
	rootFlag := flags.String("root", "", "threadmark root directory (default ~/.threadmark)")
	socketFlag := flags.String("socket", "", "unix socket path (default <root>/daemon.sock)")
	daemonFlag := flags.String("daemon-command", "", "threadmarkd command path (default sibling binary or PATH)")
	timeout := flags.Duration("timeout", 2*time.Second, "shutdown/startup timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	root, socketPath, err := daemonPaths(*rootFlag, *socketFlag)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark daemon restart: %v\n", err)
		return 1
	}
	if err := stopDaemon(root, socketPath, *timeout); err != nil {
		fmt.Fprintf(stderr, "threadmark daemon restart: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := ensureDaemon(ctx, root, socketPath, *daemonFlag); err != nil {
		fmt.Fprintf(stderr, "threadmark daemon restart: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "threadmarkd restarted on %s\n", socketPath)
	return 0
}

func daemonStatusCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	rootFlag := flags.String("root", "", "threadmark root directory (default ~/.threadmark)")
	socketFlag := flags.String("socket", "", "unix socket path (default <root>/daemon.sock)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	root, socketPath, err := daemonPaths(*rootFlag, *socketFlag)
	if err != nil {
		fmt.Fprintf(stderr, "threadmark daemon status: %v\n", err)
		return 1
	}
	pid, pidErr := readDaemonPID(root)
	if socketAvailable(socketPath) {
		if pidErr == nil {
			fmt.Fprintf(stdout, "threadmarkd running pid=%d socket=%s\n", pid, socketPath)
		} else {
			fmt.Fprintf(stdout, "threadmarkd running socket=%s pid=unknown\n", socketPath)
		}
		return 0
	}
	if pidErr == nil {
		fmt.Fprintf(stdout, "threadmarkd not reachable; stale pid=%d socket=%s\n", pid, socketPath)
	} else {
		fmt.Fprintf(stdout, "threadmarkd not running socket=%s\n", socketPath)
	}
	return 0
}

func daemonPaths(rootFlag, socketFlag string) (string, string, error) {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return "", "", err
	}
	socketPath, err := resolveSocket(root, socketFlag)
	if err != nil {
		return "", "", err
	}
	return root, socketPath, nil
}

func stopDaemon(root, socketPath string, timeout time.Duration) error {
	if !socketAvailable(socketPath) {
		_ = removeDaemonPID(root)
		return nil
	}

	pid, err := readDaemonPID(root)
	if err != nil {
		return fmt.Errorf("daemon is reachable but %s is missing or invalid; stop it manually or restart after the next auto-spawn: %w", daemonPIDPath(root), err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find daemon process %d: %w", pid, err)
	}
	if err := verifyDaemonProcess(pid, root, socketPath); err != nil {
		return fmt.Errorf("refusing to signal daemon pid %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			_ = removeDaemonPID(root)
			return nil
		}
		return fmt.Errorf("signal daemon process %d: %w", pid, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !socketAvailable(socketPath) {
			_ = removeDaemonPID(root)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not stop within %s", timeout)
}

func writeDaemonPID(root string, pid int) error {
	if err := os.MkdirAll(root, tmjournal.DirMode); err != nil {
		return fmt.Errorf("create threadmark root: %w", err)
	}
	if err := os.Chmod(root, tmjournal.DirMode); err != nil {
		return fmt.Errorf("set threadmark root permissions: %w", err)
	}
	path := daemonPIDPath(root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d\n", pid)), tmjournal.FileMode); err != nil {
		return fmt.Errorf("write daemon pid temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace daemon pid file: %w", err)
	}
	return nil
}

func readDaemonPID(root string) (int, error) {
	data, err := os.ReadFile(daemonPIDPath(root))
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid %q", value)
	}
	return pid, nil
}

func removeDaemonPID(root string) error {
	err := os.Remove(daemonPIDPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func daemonPIDPath(root string) string {
	return filepath.Join(root, daemonPIDFile)
}
