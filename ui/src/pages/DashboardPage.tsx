import { useShallow } from "zustand/react/shallow";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { DeviceCard } from "@/features/devices/DeviceCard";
import { VersionList } from "@/features/versions/VersionList";

export function DashboardPage() {
  const order = useDevicesStore(useShallow((s) => s.order));
  const byUdid = useDevicesStore((s) => s.byUdid);

  const recent = useVersionsStore(
    useShallow((s) => s.order.slice(0, 5).map((id) => s.byId[id])),
  );

  return (
    <section>
      <h1 className="text-xl font-semibold tracking-tight">Devices</h1>
      <p className="mt-1 text-sm text-muted">Your iPhones and iPads, live over USB or Wi-Fi.</p>

      {order.length === 0 ? (
        <div className="mt-6 rounded-card border border-dashed border-line bg-card p-10 text-center">
          <div className="text-sm font-medium">No devices connected</div>
          <div className="mt-1 text-sm text-muted">
            Plug one in to pair it, or connect it on the same network.
          </div>
        </div>
      ) : (
        <div className="mt-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {order.map((udid) => {
            const device = byUdid[udid];
            return device ? <DeviceCard key={udid} device={device} /> : null;
          })}
        </div>
      )}

      {recent.length > 0 ? (
        <div className="mt-8">
          <h2 className="text-sm font-semibold text-muted">Recent backups</h2>
          <div className="mt-3">
            <VersionList versions={recent} />
          </div>
        </div>
      ) : null}
    </section>
  );
}
