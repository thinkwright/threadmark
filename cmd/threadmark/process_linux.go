//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func detachDaemonProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func verifyDaemonProcess(pid int, root, socketPath string) error {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return fmt.Errorf("read process command line: %w", err)
	}
	args := splitProcCmdline(data)
	if len(args) == 0 {
		return fmt.Errorf("process command line is empty")
	}
	if !strings.Contains(filepath.Base(args[0]), "threadmarkd") {
		return fmt.Errorf("process executable %q is not threadmarkd", args[0])
	}
	if !argvHasFlagValue(args, "-root", root) {
		return fmt.Errorf("process command line does not match root %q", root)
	}
	if !argvHasFlagValue(args, "-socket", socketPath) {
		return fmt.Errorf("process command line does not match socket %q", socketPath)
	}
	return nil
}

func splitProcCmdline(data []byte) []string {
	trimmed := strings.TrimRight(string(data), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}
