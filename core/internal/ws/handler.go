// Package ws serves /api/ws (contracts §3): a server→client-only event socket. The handler
// authenticates and validates Origin BEFORE upgrading, sends a `hello` first frame, then
// fans out bus envelopes until the client disconnects or falls behind.
package ws

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/wire"
)

const (
	subscriberBuffer = 64
	writeTimeout     = 10 * time.Second
	pingInterval     = 30 * time.Second
)

// Handler returns the /api/ws HTTP handler. authFn validates the session cookie value.
func Handler(b *bus.Bus, authFn func(sessionID string) error, serverVersion string, allowedOrigins []string, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Auth (pre-upgrade): a valid session cookie is required.
		sessionID := ""
		if c, err := r.Cookie(auth.SessionCookieName); err == nil {
			sessionID = c.Value
		}
		if err := authFn(sessionID); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// 2. Origin (pre-upgrade): strict validation.
		if !originAllowed(r, allowedOrigins) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		// 3. Upgrade. We already validated Origin, so skip the library's own check.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			log.Warn("ws accept failed", "error", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		sub := b.Subscribe(subscriberBuffer)
		defer b.Unsubscribe(sub)

		// Reader goroutine: this socket is server→client only, but we must read so close and
		// pong frames are processed; any read error means the client is gone.
		go func() {
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					cancel()
					return
				}
			}
		}()

		// First frame: hello.
		hello := wire.NewEnvelope(wire.EventHello, wire.Hello{ServerVersion: serverVersion, Time: wire.Now()})
		if err := writeJSON(ctx, conn, hello); err != nil {
			return
		}

		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				_ = conn.Close(websocket.StatusNormalClosure, "bye")
				return
			case <-sub.Dropped():
				_ = conn.Close(websocket.StatusPolicyViolation, "client too slow")
				return
			case env := <-sub.C():
				if err := writeJSON(ctx, conn, env); err != nil {
					return
				}
			case <-ticker.C:
				pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					return
				}
			}
		}
	}
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(wctx, conn, v)
}
