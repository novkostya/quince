import { Wifi, WifiOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Device } from "@/lib/types";
import { useDeviceOp, type StartFn } from "./useDeviceOp";
import { OpNarration } from "./OpNarration";

// WifiSyncControl turns the device's Wi-Fi-sync flag on or off through quince, so setting up
// Wi-Fi backups never needs Finder (qn.7).
//
// ENABLE IS USB-ONLY, and that is a property of the setting rather than a caution. MEASURED
// 2026-07-31: with Wi-Fi sync off the device stops announcing over mDNS entirely — `idevice_id -n`
// returns nothing the moment the flag flips. So a device that NEEDS this turned on is, by
// construction, not reachable over Wi-Fi. The button is therefore disabled-with-a-reason rather
// than offered and then failed, the same shape qn.6a uses for an offline device's "Back up now".
export function WifiSyncControl({
  device,
  post,
}: {
  device: Device;
  post?: StartFn;
}) {
  const { op, starting, startError, start, inFlight } = useDeviceOp(post);

  // "unknown" means quince has not read the flag — never guess a direction from it.
  if (device.wifi_sync === "unknown") return null;

  const on = device.wifi_sync === "on";
  const onUSB = device.transports.usb != null;
  const needsUSBToEnable = !on && !onUSB;

  const submit = () =>
    start(`/devices/${device.udid}/wifi-sync`, { action: on ? "disable" : "enable" });

  return (
    <div className="flex flex-col gap-2">
      <Button
        variant="outline"
        onClick={submit}
        disabled={inFlight || needsUSBToEnable}
        title={
          needsUSBToEnable
            ? "Connect the device by cable to turn Wi-Fi sync on — with it off, the device is not reachable over Wi-Fi."
            : undefined
        }
      >
        {on ? <WifiOff size={14} /> : <Wifi size={14} />}
        {on ? "Turn off Wi-Fi sync" : "Turn on Wi-Fi sync"}
      </Button>

      {needsUSBToEnable ? (
        <p className="max-w-prose text-sm text-muted">
          Connect this device by cable to turn Wi-Fi sync on. While it is off the device does not
          announce itself over Wi-Fi, so quince cannot reach it without USB.
        </p>
      ) : null}

      {/* Turning sync OFF over Wi-Fi severs the transport the op is running on. The write lands
          before the device drops and the op verifies it by reading back — but the device then
          disappears from the Devices page, which would look like a failure without this line. */}
      {on && !onUSB ? (
        <p className="max-w-prose text-sm text-muted">
          This device is connected over Wi-Fi. Turning Wi-Fi sync off will disconnect it — it will
          reappear when you plug it in.
        </p>
      ) : null}

      <OpNarration op={op} starting={starting} startError={startError} />
    </div>
  );
}
