import { Link } from "react-router-dom";
import { useShallow } from "zustand/react/shallow";
import { Plus } from "lucide-react";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { Button } from "@/components/ui/button";
import { DeviceCard } from "@/features/devices/DeviceCard";
import { RescanButton } from "@/features/devices/RescanButton";
import { VersionList } from "@/features/versions/VersionList";
import { StorageCard } from "@/features/storage/StorageCard";
import { useStorages } from "@/features/jobs/useStorages";

export function DashboardPage() {
  const order = useDevicesStore(useShallow((s) => s.order));
  const byUdid = useDevicesStore((s) => s.byUdid);

  const recent = useVersionsStore(
    useShallow((s) => s.order.slice(0, 5).map((id) => s.byId[id])),
  );

  // "" is the DEVICE-INDEPENDENT list: storage counts and capacity are properties of the storage,
  // and only `will_be_full` needs a device. Home is not about one device, so it does not ask.
  const storages = useStorages("");
  const loadedStorages = storages.state.status === "loaded" ? storages.state.storages : [];

  return (
    <section>
      {/* THE PAGE HEADER CARRIES NO ACTION (Operator ruling, qn.6e). `Rescan` used to sit here, and
          it concerns DEVICES only — but Home is two sections now, and Storage has its own re-probe
          (`POST /api/storages/{name}/recheck`, surfaced on the card). A page-level control invites
          the reading that it does both. It does not. */}
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Home</h1>
        <p className="mt-1 text-sm text-muted">
          Your devices and where their backups live.
        </p>
      </div>

      {/* `Devices` becomes a SECTION heading now that the page is `Home` — it stopped being the
          page's name the moment storage joined it, which is the defect the rename fixes. It sits
          beside `Storage` and `Recent backups` in the same rhythm rather than being implied by the
          page title. */}
      <h2 className="mt-8 text-sm font-semibold text-muted">Devices</h2>

      {order.length === 0 ? (
        <div className="mt-3 rounded-card border border-dashed border-line bg-card p-10 text-center">
          <div className="text-sm font-medium">No devices connected</div>
          <div className="mt-1 text-sm text-muted">
            Connect a device over USB to pair it. Once paired, it shows up here while it's connected.
          </div>
        </div>
      ) : (
        <div className="mt-3 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {order.map((udid) => {
            const device = byUdid[udid];
            return device ? <DeviceCard key={udid} device={device} /> : null;
          })}
        </div>
      )}

      {/* AT THE FOOT OF ITS OWN SECTION, which is where the thing it acts on is. It renders in both
          branches above deliberately: the empty state is when you most want to rescan, and a
          control that disappears exactly when it is needed is worse than none. */}
      <div className="mt-3">
        <RescanButton />
      </div>

      {/* STORAGE SITS BESIDE DEVICES ON HOME, not in a third nav tab (Operator ruling, quince#443).
          Below the devices rather than above: a household opens quince to see whether its phones
          backed up; where those backups live answers the next question, not the first.

          IT NOW RENDERS WHEN THE LIST IS EMPTY, which it did not before (qn.6e). The condition was
          `length > 0`, so a zero-storage install showed nothing here — and that is precisely the
          state `Add storage` exists for. A section that vanishes when its one affordance is most
          needed is worse than an empty one.

          Still hidden while LOADING or FAILED, and that half is unchanged: the selector already
          surfaces a storages-load failure where it costs the user something — it decides where a
          backup lands — and repeating it here would put an error banner on Home for a section that
          is informational. `loaded` is therefore checked EXPLICITLY rather than inferred from the
          list being non-empty; those were the same condition until this rung and are not any more. */}
      {storages.state.status === "loaded" ? (
        <div className="mt-8">
          <h2 className="text-sm font-semibold text-muted">Storage</h2>
          {loadedStorages.length > 0 ? (
            <div className="mt-3 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {loadedStorages.map((s) => (
                // Keyed by NAME, not id: an unreachable storage's id is EMPTY (quince#570), so two
                // unplugged disks would collide on "". Name is the config's own key and always exists.
                <StorageCard key={s.name} storage={s} showDefault={loadedStorages.length > 1} />
              ))}
            </div>
          ) : (
            <div className="mt-3 rounded-card border border-dashed border-line bg-card p-10 text-center">
              <div className="text-sm font-medium">No storage yet</div>
              <div className="mt-1 text-sm text-muted">
                quince needs somewhere to keep backups. Add a folder it can reach.
              </div>
            </div>
          )}
          {/* At the FOOT of its own section, matching Rescan under Devices. Not in a row with it:
              Rescan is idempotent and free, this writes config, and adjacency would imply they are
              the same kind of act.

              A LINK, NOT A DIALOG TRIGGER (quince#846). The placement is unchanged and deliberate:
              a ghost button at the section foot, with `-ml-3` cancelling the size's `px-3` inset so
              the label starts at the column margin — a ghost button has no background, so without
              it the label reads as a stray indent. `asChild` keeps that appearance on a real
              anchor, which is long-pressable, middle-clickable, and has a URL. */}
          <div className="mt-3">
            <Button asChild variant="ghost" size="sm" className="-ml-3">
              <Link to="/storage/new" data-testid="add-storage">
                <Plus size={14} />
                Add storage
              </Link>
            </Button>
          </div>
        </div>
      ) : null}

      {recent.length > 0 ? (
        <div className="mt-8">
          <h2 className="text-sm font-semibold text-muted">Recent backups</h2>
          <div className="mt-3">
            {/* showDevice: this list mixes devices, so each row names its device (qn.6a #3). */}
            <VersionList versions={recent} showDevice />
          </div>
        </div>
      ) : null}
    </section>
  );
}
