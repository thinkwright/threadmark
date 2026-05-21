//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func detachDaemonProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func verifyDaemonProcess(pid int, root, socketPath string) error {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return fmt.Errorf("read process command line: %w", err)
	}
	cmdline := strings.TrimSpace(string(output))
	if cmdline == "" {
		return fmt.Errorf("process command line is empty")
	}
	if !strings.Contains(cmdline, "threadmarkd") {
		return fmt.Errorf("process command line %q is not threadmarkd", cmdline)
	}
	if !commandLineHasFlagValue(cmdline, "-root", root) {
		return fmt.Errorf("process command line does not match root %q", root)
	}
	if !commandLineHasFlagValue(cmdline, "-socket", socketPath) {
		return fmt.Errorf("process command line does not match socket %q", socketPath)
	}
	return nil
}
