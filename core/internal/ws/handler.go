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

// pingInterval is the liveness beat, and since qn.13 it is also how often the principal is
// re-resolved — so it bounds how long a revoked session or a changed scope keeps its socket.
//
// A VAR SO TESTS CAN SHRINK IT. The alternative is a test that waits a real 30 seconds, and
// this codebase already injects clocks rather than sleeping through them (qn.12 G3). The
// production value lives here and nothing else writes it.
var pingInterval = 30 * time.Second

const (
	subscriberBuffer = 64
	writeTimeout     = 10 * time.Second
)

// Handler returns the /api/ws HTTP handler. authFn validates the session cookie value.
// AUTHFN RETURNS A PRINCIPAL, NOT JUST AN ERROR, AND THAT IS THE POINT OF THE CHANGE.
//
// This socket bypasses the JSON API chain — `server.go` says so — so it never passed through
// `authGuard`, which is where qn.13 slice 3 binds the principal. Its own auth closure read
// `if _, err := Authenticate(...)`, discarding the session: the exact pattern quince#1342
// described and slice 3 fixed everywhere ELSE. Slice 3 claimed the session was no longer
// discarded; that was true of `authGuard` and false here.
//
// It matters more here than at any single route. The bus carries device.attached,
// device.updated, job.updated, job.log, op.updated and version.* — and this loop wrote EVERY
// envelope to EVERY client. A device-scoped holder would have received every device in the
// household by name, model and iOS version, plus every job and every version, without doing
// anything wrong. Spec D8 says the devices list must be unreachable rather than merely
// unlinked; a socket streaming all of it defeats that.
func Handler(b *bus.Bus, authFn func(sessionID string) (Principal, error), serverVersion string, allowedOrigins []string, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Auth (pre-upgrade): a valid session cookie is required.
		sessionID := ""
		if c, err := r.Cookie(auth.SessionCookieName); err == nil {
			sessionID = c.Value
		}
		principal, err := authFn(sessionID)
		if err != nil {
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
				// THE FILTER IS AT SEND, AND THE PRINCIPAL IS RE-RESOLVED ON THE PING TICK BELOW.
				//
				// Send-versus-subscribe decides WHERE a scope is applied, not whether it is fresh —
				// a principal resolved once at connect is stale in both designs (quince#1380
				// review). Freshness is the ticker's job; this line is where the answer is used.
				if !principal.MayReceive(env) {
					continue
				}
				if err := writeJSON(ctx, conn, env); err != nil {
					return
				}
			case <-ticker.C:
				// RE-RESOLVE BEFORE PINGING, so a scope change or a revocation takes effect within
				// one interval instead of lasting as long as the connection.
				//
				// THIS ALSO ENDS A SOCKET WHOSE SESSION IS GONE, which was true before qn.13 and
				// nobody had cause to notice: `authFn` ran once, pre-upgrade, so a client that
				// logged out — or whose session quince#1001 deleted when its credential was
				// removed — kept receiving every frame until it disconnected on its own.
				//
				// THE PING IS THE RIGHT PLACE rather than a second timer: it is the existing
				// liveness beat, so the cost is one session lookup per interval per socket and no
				// new goroutine. Bounded staleness, stated: up to one `pingInterval`.
				fresh, err := authFn(sessionID)
				if err != nil {
					_ = conn.Close(websocket.StatusPolicyViolation, "session ended")
					return
				}
				principal = fresh
				pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
				perr := conn.Ping(pctx)
				pcancel()
				if perr != nil {
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
