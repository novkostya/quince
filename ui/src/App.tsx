import { useState } from "react";
import { HardDrive, Settings as SettingsIcon, type LucideIcon } from "lucide-react";
import { useHealth, type ConnState } from "./lib/useHealth";

type NavKey = "devices" | "settings";

const NAV: { key: NavKey; label: string; icon: LucideIcon }[] = [
  { key: "devices", label: "Devices", icon: HardDrive },
  { key: "settings", label: "Settings", icon: SettingsIcon },
];

export default function App() {
  const [active, setActive] = useState<NavKey>("devices");
  const { conn, health } = useHealth();

  return (
    <div className="flex h-full min-h-screen bg-bg text-fg">
      <Sidebar active={active} onNavigate={setActive} conn={conn} version={health?.version} />
      <main className="flex-1 overflow-auto p-8">
        {active === "devices" ? <DevicesStub /> : <SettingsStub />}
      </main>
    </div>
  );
}

function Sidebar({
  active,
  onNavigate,
  conn,
  version,
}: {
  active: NavKey;
  onNavigate: (k: NavKey) => void;
  conn: ConnState;
  version: string | undefined;
}) {
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
        {NAV.map(({ key, label, icon: Icon }) => {
          const isActive = active === key;
          return (
            <button
              key={key}
              type="button"
              onClick={() => onNavigate(key)}
              aria-current={isActive ? "page" : undefined}
              className={
                "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors " +
                (isActive
                  ? "bg-accent-soft font-medium text-accent"
                  : "text-muted hover:bg-elevated hover:text-fg")
              }
            >
              <Icon size={18} strokeWidth={1.75} />
              {label}
            </button>
          );
        })}
      </nav>

      <div className="mt-auto px-5 py-4">
        <ConnBadge conn={conn} />
      </div>
    </aside>
  );
}

function ConnBadge({ conn }: { conn: ConnState }) {
  const map: Record<ConnState, { dot: string; label: string }> = {
    connecting: { dot: "bg-warn", label: "connecting…" },
    online: { dot: "bg-ok", label: "connected" },
    offline: { dot: "bg-danger", label: "offline" },
  };
  const { dot, label } = map[conn];
  return (
    <div className="flex items-center gap-2 text-xs text-muted" role="status">
      <span className={`inline-block h-2 w-2 rounded-full ${dot}`} aria-hidden />
      {label}
    </div>
  );
}

function DevicesStub() {
  return (
    <section>
      <h1 className="text-xl font-semibold tracking-tight">Devices</h1>
      <p className="mt-1 text-sm text-muted">
        Your iPhones and iPads appear here the moment they connect — over USB or Wi-Fi.
      </p>
      <div className="mt-6 rounded-card border border-dashed border-line bg-card p-10 text-center">
        <div className="text-sm font-medium">No devices yet</div>
        <div className="mt-1 text-sm text-muted">
          Plug a device in to pair it, or connect it on the same network.
        </div>
      </div>
    </section>
  );
}

function SettingsStub() {
  return (
    <section>
      <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
      <p className="mt-1 text-sm text-muted">
        Everything quince does is configured in one tidy <code className="font-mono">config.yml</code>;
        this page will edit it. Coming in qn.1.
      </p>
    </section>
  );
}
