# quince — improvement proposals ledger

> The non-blocking sibling of the gap protocol — rules in
> [`program/quince.program.md`](program/quince.program.md) ("Improvement proposals").
> At most one per rung, filed at rung end, never implemented before an `accepted`
> ruling. Declined entries stay here with their reason — **read them before filing;
> they are the project's accumulated taste.**

Entry format:

```
## P<N> — <short title>            [proposed: qn.X, YYYY-MM-DD] [status: proposed]
Problem:   <one line>
Sketch:    <one line>
Value:     <one line — which of correctness/reliability/security/UX/maintenance, and how much>
Cost:      S | M | L
Ruling:    <filled by Operator: accepted → qn.Y / declined: <why> / parked>
```

---

## P1 — onboarding/health check for broken container USB access   [proposed: qn.2b, 2026-07-20] [status: proposed]
Problem:   with `manage_muxer: true` the muxer reports `running` while unable to OPEN any device (frozen container `/dev`, missing cgroup perms) — silent until the user wonders why no device appears; exactly the qn.2b staging bug, subtle and hours to diagnose.
Sketch:    when a USB device is present in `/sys/bus/usb` but usbmuxd logs `LIBUSB_ERROR_NO_DEVICE` / enumerates zero, surface an actionable `/api/health` + onboarding warning ("USB muxer can't open devices — compose needs a LIVE /dev/bus/usb bind + privileged/device_cgroup_rules") linking the deploy docs.
Value:     reliability/UX — turns a silent, copy-the-wrong-`devices:`-line failure into a one-line guided fix at setup; the D12 Plex-bar promise (§9 onboarding "usbmuxd reachable" check) depends on catching exactly this.
Cost:      M — needs usbmuxd enumeration/log parsing + a health/onboarding surface; the onboarding framework (§9) isn't built until qn.6, so it lands naturally there.
Ruling:    <pending>

