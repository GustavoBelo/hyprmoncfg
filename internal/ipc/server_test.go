package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/crmne/hyprmoncfg/internal/apply"
	"github.com/crmne/hyprmoncfg/internal/appstatus"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

type testHandler struct {
	mu           sync.Mutex
	document     appstatus.Document
	preview      Transaction
	previewErr   error
	revertErr    error
	disconnected []string
}

func (h *testHandler) Status() (appstatus.Document, error) { return h.document, nil }

func (h *testHandler) Preview(_ string, _ PreviewParams) (Transaction, error) {
	return h.preview, h.previewErr
}

func (h *testHandler) Confirm(_ string, _ TransactionParams) error { return nil }
func (h *testHandler) Revert(_ string, _ TransactionParams) error  { return h.revertErr }
func (h *testHandler) Save(_ SaveParams) error                     { return nil }
func (h *testHandler) Delete(_ DeleteParams) error                 { return nil }

func (h *testHandler) Disconnect(owner string) {
	h.mu.Lock()
	h.disconnected = append(h.disconnected, owner)
	h.mu.Unlock()
}

func (h *testHandler) disconnectCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.disconnected)
}

func runTestServer(t *testing.T, handler Handler) (*Server, string, context.CancelFunc) {
	t.Helper()
	path := filepath.Join(t.TempDir(), SocketName)
	server := &Server{Path: path, Handler: handler}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("IPC socket was not created")
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server did not shut down")
		}
	})
	return server, path, cancel
}

func TestClientStatusAndPreview(t *testing.T) {
	handler := &testHandler{
		document: appstatus.Document{
			SchemaVersion: 1,
			Version:       "test",
			Daemon:        appstatus.Daemon{Running: true},
		},
		preview: Transaction{
			ID:       "transaction-1",
			Profile:  profile.New("desk", nil),
			Deadline: time.Now().Add(10 * time.Second).UTC(),
		},
	}
	_, path, _ := runTestServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	document, err := client.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if document.Version != "test" || !document.Daemon.Running {
		t.Fatalf("unexpected status: %+v", document)
	}
	transaction, err := client.Preview(ctx, PreviewParams{ProfileName: "desk", TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if transaction.ID != "transaction-1" || transaction.Profile.Name != "desk" {
		t.Fatalf("unexpected transaction: %+v", transaction)
	}
}

func TestClientPreservesUnmanagedConfigError(t *testing.T) {
	handler := &testHandler{
		document: appstatus.Document{},
		previewErr: &apply.UnmanagedMonitorConfigError{
			Path:            "/tmp/monitors.conf",
			AlternativePath: "/tmp/hyprmoncfg.conf",
		},
	}
	_, path, _ := runTestServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.Preview(ctx, PreviewParams{ProfileName: "desk"})
	var unmanaged *apply.UnmanagedMonitorConfigError
	if !errors.As(err, &unmanaged) {
		t.Fatalf("expected unmanaged config error, got %v", err)
	}
	if unmanaged.Path != "/tmp/monitors.conf" || unmanaged.AlternativePath != "/tmp/hyprmoncfg.conf" {
		t.Fatalf("unexpected error details: %+v", unmanaged)
	}
}

func TestClientPreservesUnavailableTransactionError(t *testing.T) {
	handler := &testHandler{revertErr: ErrTransactionUnavailable}
	_, path, _ := runTestServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	err = client.Revert(ctx, "expired")
	if !errors.Is(err, ErrTransactionUnavailable) {
		t.Fatalf("expected transaction unavailable error, got %v", err)
	}
}

func TestSubscribeReceivesStatusEvents(t *testing.T) {
	handler := &testHandler{document: appstatus.Document{SchemaVersion: 1, Version: "event"}}
	server, path, _ := runTestServer(t, handler)

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)
	if err := encoder.Encode(Request{Type: "request", ProtocolVersion: 1, ID: "1", Method: MethodSubscribe}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("subscribe failed: %+v", response.Error)
	}

	server.Notify()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "event" || event.Event != EventStatus || event.ProtocolVersion != ProtocolVersion {
		t.Fatalf("unexpected event: %+v", event)
	}
	var document appstatus.Document
	if err := json.Unmarshal(event.Data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "event" {
		t.Fatalf("unexpected event document: %+v", document)
	}
}

func TestShutdownClosesClientsAndWaitsForDisconnect(t *testing.T) {
	handler := &testHandler{}
	_, path, cancel := runTestServer(t, handler)
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(Request{Type: "request", ProtocolVersion: 1, ID: "1", Method: MethodStatus}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for handler.disconnectCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if handler.disconnectCount() != 1 {
		t.Fatalf("disconnect calls = %d, want 1", handler.disconnectCount())
	}
}
