package main

import "testing"

func TestCommandLineHasFlagValueRequiresExactTokenBoundary(t *testing.T) {
	commandLine := "threadmarkd -root /tmp/threadmark-other -socket=/tmp/threadmark/daemon.sock-extra"

	if commandLineHasFlagValue(commandLine, "-root", "/tmp/threadmark") {
		t.Fatal("matched root value prefix from longer flag value")
	}
	if commandLineHasFlagValue(commandLine, "-socket", "/tmp/threadmark/daemon.sock") {
		t.Fatal("matched socket value prefix from longer flag value")
	}
}

func TestCommandLineHasFlagValueMatchesSeparateAndEqualsForms(t *testing.T) {
	commandLine := "threadmarkd -root /tmp/threadmark -socket=/tmp/threadmark/daemon.sock"

	if !commandLineHasFlagValue(commandLine, "-root", "/tmp/threadmark") {
		t.Fatal("did not match separate flag value")
	}
	if !commandLineHasFlagValue(commandLine, "-socket", "/tmp/threadmark/daemon.sock") {
		t.Fatal("did not match equals flag value")
	}
}
