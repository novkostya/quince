import type { Device, Storage } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { useBackup } from "@/features/jobs/useBackup";

// StorageDeviceBackup is `Back up now` on the STORAGE details page — story 6.
//
// It is `qn.6c` story 9's selector answered by CONTEXT instead of a dropdown: the page you are on
// IS the destination, so the job is bound to this storage and there is nothing to choose. That is
// also why this is a component per device rather than one control for the page — `useBackup` is
// keyed by udid and hooks cannot be called in a loop.
//
// IT REFUSES BEFORE THE SERVER HAS TO. A storage whose resolution did not succeed never accepts a
// job (`Slot.Usable()`, design §5), so offering the action on an unreachable storage would earn a
// 409 and teach the user that the button is unreliable rather than that the disk is unplugged.
// Same for a storage with no id: one that was never created cannot be a job's destination.
export function StorageDeviceBackup({ device, storage }: { device: Device; storage: Storage }) {
  const { start, busy, error } = useBackup(device.udid);

  const present = Boolean(device.transports.usb || device.transports.wifi);
  const blocked = !storage.reachable
    ? "not connected — plug this storage in to back up to it"
    : storage.id === ""
      ? "quince has never reached this storage, so it cannot be a destination yet"
      : !present
        ? "connect this device over USB or Wi-Fi to back it up"
        : null;

  return (
    <div className="mt-2">
      <Button
        size="sm"
        variant="outline"
        data-testid="storage-device-backup"
        data-udid={device.udid}
        disabled={blocked !== null || busy}
        onClick={() => {
          // The storage is the PAGE's, never a selection — which is the whole of story 6. `auto`
          // lets the engine resolve the transport, exactly as the device page does.
          void start("auto", { storageID: storage.id });
        }}
      >
        Back up now
      </Button>
      {/* The reason sits BELOW the control, never inside the row (quince#325: the action row holds
          controls, prose goes beneath). A disabled button with no reason is the state this project
          has already had to fix once. */}
      {blocked !== null ? (
        <div data-testid="storage-device-backup-blocked" className="mt-1 text-xs text-muted">
          {blocked}
        </div>
      ) : null}
      {error !== null ? (
        <div role="alert" className="mt-1 text-xs text-danger">
          {error}
        </div>
      ) : null}
    </div>
  );
}
