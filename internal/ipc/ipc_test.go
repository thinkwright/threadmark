package ipc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thinkwright/threadmark/internal/core"
)

func TestServerReceivesNDJSONEvent(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	received := make(chan core.Event, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := Server{
		SocketPath: socketPath,
		Handler: HandlerFunc(func(_ context.Context, event core.Event) error {
			received <- event
			return nil
		}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()

	waitForSocket(t, socketPath)

	event := testEvent(t, "evt_ipc_one")
	sendCtx, sendCancel := context.WithTimeout(context.Background(), time.Second)
	defer sendCancel()
	if err := Send(sendCtx, socketPath, event); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	select {
	case got := <-received:
		if got.EventID != event.EventID {
			t.Fatalf("received event id = %q, want %q", got.EventID, event.EventID)
		}
		if got.Ts.Location() != time.UTC {
			t.Fatalf("received timestamp location = %v, want UTC", got.Ts.Location())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ListenAndServe returned error after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server shutdown")
	}
}

func TestServerAcceptsMultipleEventsOnOneConnection(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	received := make(chan string, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := Server{
		SocketPath: socketPath,
		Handler: HandlerFunc(func(_ context.Context, event core.Event) error {
			received <- event.EventID
			return nil
		}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()
	waitForSocket(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	for _, id := range []string{"evt_first", "evt_second"} {
		encoded, err := json.Marshal(testEvent(t, id))
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		if _, err := conn.Write(append(encoded, '\n')); err != nil {
			t.Fatalf("write event: %v", err)
		}
	}
	_ = conn.Close()

	for _, want := range []string{"evt_first", "evt_second"} {
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("received event id = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("ListenAndServe returned error after cancellation: %v", err)
	}
}

func TestSendManySendsEventsOnOneConnectionInOrder(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	received := make(chan string, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := Server{
		SocketPath: socketPath,
		Handler: HandlerFunc(func(_ context.Context, event core.Event) error {
			received <- event.EventID
			return nil
		}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()
	waitForSocket(t, socketPath)

	sendCtx, sendCancel := context.WithTimeout(context.Background(), time.Second)
	defer sendCancel()
	if err := SendMany(sendCtx, socketPath, []core.Event{
		testEvent(t, "evt_batch_first"),
		testEvent(t, "evt_batch_second"),
	}); err != nil {
		t.Fatalf("SendMany returned error: %v", err)
	}

	for _, want := range []string{"evt_batch_first", "evt_batch_second"} {
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("received event id = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("ListenAndServe returned error after cancellation: %v", err)
	}
}

func TestSendManyRetriesUntilServerAppears(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	received := make(chan string, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := Server{
		SocketPath: socketPath,
		Handler: HandlerFunc(func(_ context.Context, event core.Event) error {
			received <- event.EventID
			return nil
		}),
	}
	errCh := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		errCh <- server.ListenAndServe(ctx)
	}()

	sendCtx, sendCancel := context.WithTimeout(context.Background(), time.Second)
	defer sendCancel()
	event := testEvent(t, "evt_retry_until_server")
	if err := SendMany(sendCtx, socketPath, []core.Event{event}); err != nil {
		t.Fatalf("SendMany returned error: %v", err)
	}

	select {
	case got := <-received:
		if got != event.EventID {
			t.Fatalf("received event id = %q, want %q", got, event.EventID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retried event")
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("ListenAndServe returned error after cancellation: %v", err)
	}
}

func TestSendRejectsInvalidEventBeforeDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := Send(ctx, filepath.Join(t.TempDir(), "missing.sock"), core.Event{})
	if err == nil {
		t.Fatal("Send returned nil error")
	}
}

func TestServerReportsInvalidLines(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	rejected := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := Server{
		SocketPath: socketPath,
		ErrorHandler: func(err error) {
			rejected <- err
		},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()
	waitForSocket(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	if _, err := conn.Write([]byte("{bad json\n")); err != nil {
		t.Fatalf("write invalid event: %v", err)
	}
	_ = conn.Close()

	select {
	case err := <-rejected:
		if err == nil {
			t.Fatal("error handler received nil")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rejected event")
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("ListenAndServe returned error after cancellation: %v", err)
	}
}

func TestPrepareSocketRejectsNonSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write non-socket file: %v", err)
	}

	if err := prepareSocket(path); err == nil {
		t.Fatal("prepareSocket returned nil error")
	}
}

func testEvent(t *testing.T, id string) core.Event {
	t.Helper()
	event, err := core.NewEvent(
		core.KindText,
		"codex",
		"session-1",
		"/workspace/threadmark",
		core.TextPayload{Actor: core.ActorUser, Content: "hello"},
		core.WithEventID(id),
		core.WithTimestamp(time.Date(2026, 5, 18, 12, 0, 0, 123, time.FixedZone("offset", -7*60*60))),
	)
	if err != nil {
		t.Fatalf("NewEvent returned error: %v", err)
	}
	return event
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		info, err := os.Lstat(socketPath)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for socket %s", socketPath)
}
