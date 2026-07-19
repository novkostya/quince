package ws

import (
	"net/http"
	"net/url"
	"strings"
)

// originAllowed enforces strict WS Origin validation (design §6). A browser always sends
// Origin on the WebSocket handshake; we require it to be same-origin (Origin host == Host)
// or in the configured allowlist. A missing Origin is rejected — real browsers never omit
// it, and rejecting it closes the cross-origin hole. allowed entries may be full origins
// ("https://nas.local") or bare hosts ("nas.local:8443").
func originAllowed(r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, a := range allowed {
		if strings.EqualFold(a, origin) || strings.EqualFold(a, u.Host) {
			return true
		}
	}
	return false
}
