package main

import (
	"context"
	"log/slog"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/httpapi"
	"github.com/novkostya/quince/core/internal/muxaddr"
	"github.com/novkostya/quince/core/internal/muxd"
	"github.com/novkostya/quince/core/internal/muxsup"
)

// THE MANAGED ARMS BELOW ARE PARKED IN v0.1, NOT BROKEN (qn.6p D1/D2). `devices.manage_muxer:
// true` is refused by config validation, so on a shipping build plannedMuxers only ever takes its
// external arms and `plan.supervise` is always empty. They are kept — with their tests — against
// the return of the all-in-one profile, which the Operator descoped rather than abandoned. Read
// them as waiting, not as scaffolding, and do not delete them to satisfy a coverage number.
//
// The muxer topology quince runs (stack D2, qn.2b + qn.4c). `devices.manage_muxer: true` means
// "quince owns the muxers it is configured to reach" — ONE flag governing every daemon, not one
// flag per daemon (D12 config tidiness; ruled (bz)-2): usbmuxd when devices.usbmuxd_socket is set,
// netmuxd when devices.netmuxd_addr is set. The mixed topology (manage one, dial an external
// other) is not expressible as a flag, but it is handled: the supervisor's refuse-loudly probe
// finds the address already served, does not start a competitor, and reports that daemon degraded
// while the muxd client keeps dialing it.

// externalMuxer is a daemon quince only dials — reported in /api/health so an operator never has
// to infer a muxer's existence from its absence.
type externalMuxer struct{ address string }

// muxerPlan is what a configuration asks for, computed without side effects so it can be tested
// directly (the supervisors themselves spawn processes).
type muxerPlan struct {
	supervise []muxsup.Spec
	external  []externalMuxer
	problems  []string // loud misconfigurations: never silently swallowed, never built around
}

// plannedMuxers resolves the `muxers:` list into a plan. In v0.1 every entry is EXTERNAL: quince
// dials it and reports what it finds, and owns no daemon.
//
// THE MANAGED ARM IS GONE FROM THIS FUNCTION AND THE SUPERVISION IS NOT (quince#1219 A/B/C).
// What was deleted is the CONFIG → SPEC MAPPING, and it was deleted because it can no longer be
// written down: a supervised daemon needs a NAME to know whether it is `usbmuxd -f -S <socket>` or
// `netmuxd --host/--port … --disable-usb`, and the ruled schema has `address:` and `type:` and no
// `daemon:`. The old mapping got that name from WHICH KEY the address came out of — the same
// assumption item E deleted from health — so under a list there is nothing to read it from.
//
// It was already unreachable before this change: `devices.manage_muxer: true` has been a fatal
// serve-path refusal since qn.6p, and `type: managed` is refused the same way now. So no behaviour
// changes, and the parked work is untouched — `muxsup.Usbmuxd`, `muxsup.Netmuxd`, `Supervisor` and
// their hardware-proven tests all remain, including the netmuxd socket-collision refusal (netmuxd
// DELETES and rebinds whatever `--socket-path` names, which would silently kill USB — the qn.4c
// spike finding). Reintroducing the profile means giving the schema a way to name a daemon and
// wiring these specs back up, not re-earning proof that already exists.
func plannedMuxers(muxers []config.MuxerConfig) muxerPlan {
	var p muxerPlan
	seen := map[string]bool{}
	for _, m := range muxers {
		// THE CANONICAL SPELLING, NOT THE WRITTEN ONE, and this is the seam where it matters.
		// `/run/mux/usbmuxd` and `UNIX:/run/mux/usbmuxd` are the same daemon written two ways
		// (muxaddr's grammar). The registry keys presence by `Endpoint.String()`, health reports
		// this address as the muxer's identity, and item E looks transports up BY that address —
		// so if health reported what the operator typed while the registry recorded the canonical
		// form, a muxer written the second way would report `transports: []` forever while
		// devices attached over it.
		ep, err := muxaddr.Parse(m.Address)
		if err != nil || ep.IsZero() {
			continue // refused by Validate and again on the serve path; nothing to dial
		}
		addr := ep.String()
		if seen[addr] {
			continue // one connection per muxer; an exact duplicate is a validation error
		}
		seen[addr] = true
		p.external = append(p.external, externalMuxer{addr})
	}
	return p
}

// buildMuxerGroup turns the plan into a runnable group, logging what quince owns, what it merely
// dials, and every problem it refused to build around.
// `dialerFor` maps a CONFIGURED address to the client holding that connection, or nil when
// nothing dials it. It is passed in rather than reached for so this function keeps no
// dependency on how the clients are built, and so plannedMuxers stays the side-effect-free
// half that tests drive directly.
//
// IT MUST RETURN AN UNTYPED NIL when there is no client. Returning a nil *muxd.Client would
// yield a NON-nil interface holding a nil pointer, so muxsup's `dialer == nil` check would
// pass and it would call Health() on nothing — turning the wiring bug status() is careful to
// REPORT into a panic. live.go's lookup returns a literal nil for exactly this reason.
func buildMuxerGroup(muxers []config.MuxerConfig, declared bool, dialerFor func(address string) muxsup.Dialer,
	log *slog.Logger) *muxsup.Group {
	plan := plannedMuxers(muxers)
	g := muxsup.NewGroup()
	for _, spec := range plan.supervise {
		g.Supervise(muxsup.New(spec, log))
	}
	for _, e := range plan.external {
		var dialer muxsup.Dialer
		if dialerFor != nil {
			dialer = dialerFor(e.address)
		}
		g.AddUnmanaged(e.address, declared, dialer)
		log.Info("muxer is external — dialing only", "address", e.address)
	}
	for _, problem := range plan.problems {
		log.Error("muxsup: " + problem)
	}
	if names := g.Names(); names != "" {
		log.Info("supervising in-container muxers", "daemons", names)
	}
	return g
}

// muxerHealth adapts muxsup's group to the httpapi seam (httpapi defines the wire shape; muxsup
// stays free of HTTP types).
type muxerHealth struct{ g *muxsup.Group }

func (m muxerHealth) Rescan(ctx context.Context) (bool, string) { return m.g.Rescan(ctx) }

func (m muxerHealth) MuxersHealth() []httpapi.MuxerHealth {
	statuses := m.g.Statuses()
	out := make([]httpapi.MuxerHealth, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, httpapi.MuxerHealth{
			Address: s.Address, Transports: transportsOrEmpty(s.Transports), Managed: s.Managed,
			State: s.State, Detail: s.Detail, Rescan: s.Rescan,
		})
	}
	return out
}

// transportsOrEmpty keeps `transports` a LIST in the payload even when a muxer is serving nothing.
// A nil slice marshals to `null`, which a client reads as "unknown"; `[]` is the measurement —
// this muxer is reachable and no device is coming over it (quince#1219 item E).
func transportsOrEmpty(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

// dialerLookup maps a muxer's CANONICAL address to the client holding that connection, for
// buildMuxerGroup. Canonical because that is what plannedMuxers carries, what the registry uses as
// its source id, and what health reports as the muxer's identity — see plannedMuxers for why all
// three must be the same string.
//
// A NAMED FUNCTION RATHER THAN A CLOSURE, so the nil case can be tested (architect, quince#1060).
// It must return a LITERAL nil when nothing dials the address: returning a nil *muxd.Client would
// give muxsup a non-nil interface holding a nil pointer, its `dialer == nil` check would pass, and
// it would call Health() on nothing — turning the wiring bug status() is careful to REPORT into a
// panic. The comment was the only thing holding that before; now a test does.
//
// IT TOOK TWO MAPS UNTIL quince#1219, because an address had to be resolved to an endpoint before
// a client could be found: two config keys could name one daemon, so the lookup went
// configured-address → endpoint → client. The list removes the first hop — entries are
// canonicalised once, in plannedMuxers — so one map does what two did.
func dialerLookup(byAddress map[string]*muxd.Client) func(address string) muxsup.Dialer {
	return func(address string) muxsup.Dialer {
		if c, ok := byAddress[address]; ok && c != nil {
			return c
		}
		return nil
	}
}
