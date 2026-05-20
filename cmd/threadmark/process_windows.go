//go:build windows

package main

import "os/exec"

func detachDaemonProcess(cmd *exec.Cmd) {}

func verifyDaemonProcess(int, string, string) error {
	return nil
}
