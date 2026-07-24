import { NavLink } from "react-router-dom";
import { HardDrive, Settings as SettingsIcon, type LucideIcon } from "lucide-react";
import { useConnectionStore } from "@/stores/connection";
import { ConnBadge } from "./ConnBadge";

const NAV: { to: string; label: string; icon: LucideIcon }[] = [
  { to: "/devices", label: "Devices", icon: HardDrive },
  { to: "/settings", label: "Settings", icon: SettingsIcon },
];

// Responsive nav: a horizontal top bar on phones, the vertical left sidebar on desktop (qn.6a mobile
// pass). Same links, one component — Tailwind flips flex-row → flex-col at the sm breakpoint.
export function Sidebar() {
  const version = useConnectionStore((s) => s.serverVersion);
  return (
    <aside
      className="flex shrink-0 flex-row items-center gap-1 border-b border-line bg-card px-3 py-2 sm:sticky sm:top-0 sm:h-screen sm:w-[var(--sidebar-w)] sm:flex-col sm:items-stretch sm:gap-0 sm:self-start sm:border-b-0 sm:border-r sm:px-0 sm:py-0"
      aria-label="Primary"
    >
      <div className="px-1 sm:px-5 sm:pb-5 sm:pt-6">
        <div className="text-base font-semibold tracking-tight sm:text-lg">quince</div>
        <div className="hidden font-mono text-xs text-subtle sm:block" data-testid="version">
          {version ? `v${version}` : "—"}
        </div>
      </div>

      <nav className="flex flex-1 flex-row gap-1 sm:flex-none sm:flex-col sm:px-3">
        {NAV.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              "flex min-h-[40px] items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors " +
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

      <div className="px-1 sm:mt-auto sm:px-5 sm:py-4">
        <ConnBadge />
      </div>
    </aside>
  );
}
