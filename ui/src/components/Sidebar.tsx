import { NavLink } from "react-router-dom";
import { HardDrive, Settings as SettingsIcon, type LucideIcon } from "lucide-react";
import { useConnectionStore } from "@/stores/connection";
import { ConnBadge } from "./ConnBadge";

const NAV: { to: string; label: string; icon: LucideIcon }[] = [
  { to: "/devices", label: "Devices", icon: HardDrive },
  { to: "/settings", label: "Settings", icon: SettingsIcon },
];

export function Sidebar() {
  const version = useConnectionStore((s) => s.serverVersion);
  return (
    <aside
      className="flex w-[var(--sidebar-w)] shrink-0 flex-col border-r border-line bg-card"
      aria-label="Primary"
    >
      <div className="px-5 pb-5 pt-6">
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <div className="mt-0.5 font-mono text-xs text-subtle" data-testid="version">
          {version ? `v${version}` : "—"}
        </div>
      </div>

      <nav className="flex flex-col gap-1 px-3">
        {NAV.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors " +
              (isActive
                ? "bg-accent-soft font-medium text-accent"
                : "text-muted hover:bg-elevated hover:text-fg")
            }
          >
            <Icon size={18} strokeWidth={1.75} />
            {label}
          </NavLink>
        ))}
      </nav>

      <div className="mt-auto px-5 py-4">
        <ConnBadge />
      </div>
    </aside>
  );
}
