package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
)

const (
	SocketDirMode  os.FileMode = 0o700
	SocketFileMode os.FileMode = 0o600
	DefaultSocket              = "daemon.sock"
)

var sendRetryDelays = []time.Duration{
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
}

type Handler interface {
	HandleEvent(context.Context, core.Event) error
}

type HandlerFunc func(context.Context, core.Event) error

func (fn HandlerFunc) HandleEvent(ctx context.Context, event core.Event) error {
	return fn(ctx, event)
}

type Server struct {
	SocketPath   string
	Handler      Handler
	ErrorHandler func(error)
}

func DefaultSocketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}
	return filepath.Join(home, ".threadmark", DefaultSocket), nil
}

func (s Server) ListenAndServe(ctx context.Context) error {
	socketPath, err := s.socketPath()
	if err != nil {
		return err
	}
	if err := prepareSocket(socketPath); err != nil {
		return err
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on unix socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	if err := os.Chmod(socketPath, SocketFileMode); err != nil {
		return fmt.Errorf("set socket permissions: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept unix socket connection: %w", err)
		}
		go s.handleConn(ctx, conn)
	}
}

func (s Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if err := s.handleLine(ctx, line); err != nil {
				s.handleError(err)
			}
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			return
		}
	}
}

func (s Server) handleLine(ctx context.Context, line []byte) error {
	var event core.Event
	if err := json.Unmarshal(bytes.TrimSpace(line), &event); err != nil {
		return fmt.Errorf("decode event: %w", err)
	}
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate event: %w", err)
	}
	if s.Handler == nil {
		return nil
	}
	return s.Handler.HandleEvent(ctx, event)
}

func (s Server) handleError(err error) {
	if err != nil && s.ErrorHandler != nil {
		s.ErrorHandler(err)
	}
}

func Send(ctx context.Context, socketPath string, event core.Event) error {
	return SendMany(ctx, socketPath, []core.Event{event})
}

func SendMany(ctx context.Context, socketPath string, events []core.Event) error {
	if strings.TrimSpace(socketPath) == "" {
		var err error
		socketPath, err = DefaultSocketPath()
		if err != nil {
			return err
		}
	}
	if len(events) == 0 {
		return nil
	}

	var payload []byte
	for idx, event := range events {
		event = event.Normalize()
		if err := event.Validate(); err != nil {
			return fmt.Errorf("validate event %d: %w", idx, err)
		}

		encoded, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode event %d: %w", idx, err)
		}
		payload = append(payload, encoded...)
		payload = append(payload, '\n')
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := sendPayload(ctx, socketPath, payload); err != nil {
			lastErr = err
		} else {
			return nil
		}

		if ctx.Err() != nil {
			return lastErr
		}
		if !isSocketRetryable(lastErr) {
			return lastErr
		}
		if attempt >= len(sendRetryDelays) {
			return lastErr
		}
		timer := time.NewTimer(sendRetryDelays[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return lastErr
		case <-timer.C:
		}
	}
}

func sendPayload(ctx context.Context, socketPath string, payload []byte) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial threadmark daemon: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

func isSocketRetryable(err error) bool {
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE)
}

func prepareSocket(socketPath string) error {
	if strings.TrimSpace(socketPath) == "" {
		return errors.New("socket path is required")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), SocketDirMode); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(socketPath), SocketDirMode); err != nil {
		return fmt.Errorf("set socket directory permissions: %w", err)
	}

	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat socket path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("socket path exists and is not a unix socket: %s", socketPath)
	}

	conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("threadmark daemon already listening on %s", socketPath)
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

func (s Server) socketPath() (string, error) {
	if strings.TrimSpace(s.SocketPath) != "" {
		return s.SocketPath, nil
	}
	return DefaultSocketPath()
}
