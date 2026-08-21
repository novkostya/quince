package ws

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

func setup(t *testing.T) (*bus.Bus, func(string) (Principal, error), string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	sess, _, err := svc.Login("test", "1.1.1.1", "")
	if err != nil {
		t.Fatal(err)
	}
	authFn := func(id string) (Principal, error) { _, err := svc.Authenticate(id); return AdminPrincipal(), err }
	return bus.New(), authFn, sess.ID
}

func newServer(b *bus.Bus, authFn func(string) (Principal, error)) *httptest.Server {
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

// THE SOCKET ENDS WHEN ITS SESSION DOES (quince#1380 review, finding 1).
//
// `authFn` used to run once, pre-upgrade, so a client that logged out — or whose session
// quince#1001 deleted when its credential was removed — kept receiving every frame until it
// disconnected on its own. That was true before qn.13 and nobody had cause to notice; it becomes a
// confinement hole the moment a principal can be revoked.
//
// It also bounds how stale a scope may be: the re-resolve is the same tick as the ping, so a scope
// change takes effect within one interval rather than lasting the connection.
func TestSocketClosesWhenTheSessionEnds(t *testing.T) {
	// Shrink the beat rather than waiting a real 30 seconds; restored on exit so no other test
	// inherits it. Set BEFORE the handler is built, because the ticker is created at dial time.
	defer func(v time.Duration) { pingInterval = v }(pingInterval)
	pingInterval = 50 * time.Millisecond

	b := bus.New()
	var live atomic.Bool
	live.Store(true)
	authFn := func(string) (Principal, error) {
		if !live.Load() {
			return Principal{}, errGone
		}
		return AdminPrincipal(), nil
	}

	srv := httptest.NewServer(Handler(b, authFn, "1.2.3", nil,
		slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"),
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {srv.URL}}})
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// hello arrives while the session is live — the control, so a socket that closed immediately
	// could not pass this test.
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("hello: %v", err)
	}

	live.Store(false) // the session ends under the socket
	// Shrink the beat rather than waiting a real 30 seconds; restored on exit so no other test
	// inherits it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, err := conn.Read(ctx); err != nil {
			return // closed, which is the assertion
		}
	}
	t.Fatal("the socket outlived its session — a revoked client kept receiving")
}

var errGone = errors.New("session gone")
