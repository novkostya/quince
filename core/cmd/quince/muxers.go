package main

import (
	"context"
	"log/slog"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/httpapi"
	"github.com/novkostya/quince/core/internal/muxsup"
)

// The muxer topology quince runs (stack D2, qn.2b + qn.4c). `devices.manage_muxer: true` means
// "quince owns the muxers it is configured to reach" — ONE flag governing every daemon, not one
// flag per daemon (D12 config tidiness; ruled (bz)-2): usbmuxd when devices.usbmuxd_socket is set,
// netmuxd when devices.netmuxd_addr is set. The mixed topology (manage one, dial an external
// other) is not expressible as a flag, but it is handled: the supervisor's refuse-loudly probe
// finds the address already served, does not start a competitor, and reports that daemon degraded
// while the muxd client keeps dialing it.

// externalMuxer is a daemon quince only dials — reported in /api/health so an operator never has
// to infer a muxer's existence from its absence.
type externalMuxer struct{ name, role, address string }

// muxerPlan is what a configuration asks for, computed without side effects so it can be tested
// directly (the supervisors themselves spawn processes).
type muxerPlan struct {
	supervise []muxsup.Spec
	external  []externalMuxer
	problems  []string // loud misconfigurations: never silently swallowed, never built around
}

// plannedMuxers resolves the devices config into a plan. It refuses to supervise netmuxd when its
// private unix socket would collide with the usbmuxd socket: netmuxd DELETES and rebinds whatever
// --socket-path names, so that collision would silently kill USB (verified against the shipped
// v0.4.3 binary — the qn.4c spike finding). A refused netmuxd is still dialed and still reported.
func plannedMuxers(dcfg config.DevicesConfig) muxerPlan {
	var p muxerPlan
	if dcfg.UsbmuxdSocket != "" {
		if dcfg.ManageMuxer {
			p.supervise = append(p.supervise, muxsup.Usbmuxd(dcfg.UsbmuxdSocket))
		} else {
			p.external = append(p.external, externalMuxer{"usbmuxd", muxsup.RoleUSB, dcfg.UsbmuxdSocket})
		}
	}
	if dcfg.NetmuxdAddr == "" {
		return p
	}
	if !dcfg.ManageMuxer {
		p.external = append(p.external, externalMuxer{"netmuxd", muxsup.RoleWiFi, dcfg.NetmuxdAddr})
		return p
	}
	socketPath := muxsup.SocketPathFor(dcfg.UsbmuxdSocket)
	if socketPath == dcfg.UsbmuxdSocket {
		p.refuseNetmuxd(dcfg.NetmuxdAddr, "its unix socket ("+socketPath+
			") is devices.usbmuxd_socket — netmuxd would delete and rebind it, killing USB")
		return p
	}
	spec, err := muxsup.Netmuxd(dcfg.NetmuxdAddr, socketPath)
	if err != nil {
		p.refuseNetmuxd(dcfg.NetmuxdAddr, "devices.netmuxd_addr ("+dcfg.NetmuxdAddr+
			") is not a host:port address — "+err.Error())
		return p
	}
	p.supervise = append(p.supervise, spec)
	return p
}

// refuseNetmuxd records a loud refusal to SUPERVISE netmuxd while still dialing (and reporting)
// it: quince never silently drops a configured muxer, and never starts one it cannot start safely.
func (p *muxerPlan) refuseNetmuxd(addr, why string) {
	p.problems = append(p.problems, "refusing to supervise netmuxd: "+why)
	p.external = append(p.external, externalMuxer{"netmuxd", muxsup.RoleWiFi, addr})
}

// buildMuxerGroup turns the plan into a runnable group, logging what quince owns, what it merely
// dials, and every problem it refused to build around.
func buildMuxerGroup(dcfg config.DevicesConfig, log *slog.Logger) *muxsup.Group {
	plan := plannedMuxers(dcfg)
	g := muxsup.NewGroup()
	for _, spec := range plan.supervise {
		g.Supervise(muxsup.New(spec, log))
	}
	for _, e := range plan.external {
		g.AddUnmanaged(e.name, e.role, e.address)
		log.Info("muxer is external — dialing only", "daemon", e.name, "address", e.address)
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
			Name: s.Name, Role: s.Role, Managed: s.Managed,
			State: s.State, Detail: s.Detail, Rescan: s.Rescan,
		})
	}
	return out
}
