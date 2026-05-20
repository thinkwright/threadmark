// Package ipc implements the unix-domain-socket server at
// ~/.threadmark/daemon.sock that receives events from hook shims. Wire format
// is newline-delimited JSON, one event per line.
package ipc
