import { useConnectionStore, type ConnStatus } from "@/stores/connection";

const MAP: Record<ConnStatus, { dot: string; label: string }> = {
  connecting: { dot: "bg-warn", label: "connecting…" },
  online: { dot: "bg-ok", label: "connected" },
  reconnecting: { dot: "bg-warn", label: "reconnecting…" },
  offline: { dot: "bg-danger", label: "offline" },
};

// THE WORD IS DESKTOP-ONLY; THE DOT IS EVERYWHERE — Operator-suggested on quince#838, where
// "connected" was ~70px of a phone nav bar already ~15px too wide for a 375px screen.
//
// THE STATUS IS NOT LOST ON A PHONE, WHICH IS THE HALF THAT NEEDED CARE. Colour alone is not a
// statement — a coloured dot to someone who cannot distinguish it is no dot at all, and this is the
// readout that says whether the app is still talking to the daemon. So the word moves out of the
// LAYOUT and into the accessible name and the tooltip, where it stays readable, is still announced
// when it changes, and costs no width.
//
// `sr-only` RATHER THAN REMOVING THE NODE, deliberately: a screen reader announces a live region by
// reading its text CONTENT, so an element with no text has nothing to announce however well it is
// labelled. `sm:not-sr-only` brings the same node back into the layout where there is room for it.
export function ConnBadge() {
  const status = useConnectionStore((s) => s.status);
  const { dot, label } = MAP[status];
  return (
    <div
      className="flex items-center gap-2 text-xs text-muted"
      role="status"
      title={label}
      data-testid="conn-badge"
    >
      <span className={`inline-block h-2 w-2 shrink-0 rounded-full ${dot}`} aria-hidden />
      <span className="sr-only sm:not-sr-only">{label}</span>
    </div>
  );
}
