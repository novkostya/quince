import { useConnectionStore, type ConnStatus } from "@/stores/connection";

const MAP: Record<ConnStatus, { dot: string; label: string }> = {
  connecting: { dot: "bg-warn", label: "connecting…" },
  online: { dot: "bg-ok", label: "connected" },
  reconnecting: { dot: "bg-warn", label: "reconnecting…" },
  offline: { dot: "bg-danger", label: "offline" },
};

export function ConnBadge() {
  const status = useConnectionStore((s) => s.status);
  const { dot, label } = MAP[status];
  return (
    <div className="flex items-center gap-2 text-xs text-muted" role="status" data-testid="conn-badge">
      <span className={`inline-block h-2 w-2 rounded-full ${dot}`} aria-hidden />
      {label}
    </div>
  );
}
