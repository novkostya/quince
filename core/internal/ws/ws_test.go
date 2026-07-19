package ws

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

func setup(t *testing.T) (*bus.Bus, func(string) error, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.SetPassword("test"); err != nil {
		t.Fatal(err)
	}
	sess, _, err := svc.Login("test", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	authFn := func(id string) error { _, err := svc.Authenticate(id); return err }
	return bus.New(), authFn, sess.ID
}

func newServer(b *bus.Bus, authFn func(string) error) *httptest.Server {
	return httptest.NewServer(Handler(b, authFn, "1.2.3", nil, slog.New(slog.NewTextHandler(io.Discard, nil))))
}

func TestWSHelloAndDelivery(t *testing.T) {
	b, authFn, sessionID := setup(t)
	srv := newServer(b, authFn)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {auth.SessionCookieName + "=" + sessionID},
			"Origin": {srv.URL},
		},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	var hello wire.Envelope
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if hello.Type != wire.EventHello {
		t.Fatalf("first frame type = %q, want hello", hello.Type)
	}

	// Reading hello guarantees the server has subscribed; now delivery is deterministic.
	b.PublishEvent(wire.EventJobUpdated, wire.JobLogChunk{JobID: "j", Chunk: "hi"})
	var env wire.Envelope
	if err := wsjson.Read(ctx, conn, &env); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if env.Type != wire.EventJobUpdated {
		t.Fatalf("event type = %q", env.Type)
	}
}

func TestWSForeignOriginRejected(t *testing.T) {
	b, authFn, sessionID := setup(t)
	srv := newServer(b, authFn)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {auth.SessionCookieName + "=" + sessionID},
			"Origin": {"http://evil.example"},
		},
	})
	if err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("expected foreign origin to be rejected")
	}
	if resp != nil {
		status := resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if status != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
	}
}

func TestWSUnauthenticatedRejected(t *testing.T) {
	b, authFn, _ := setup(t)
	srv := newServer(b, authFn)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {srv.URL}},
	})
	if err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("expected unauthenticated dial to be rejected")
	}
	if resp != nil {
		status := resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
	}
}
