import * as React from "react";
import { Wifi, WifiOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import type { Device } from "@/lib/types";
import { useDeviceOp, type StartFn } from "./useDeviceOp";
import { OpNarration } from "./OpNarration";

// WifiSyncControl turns the device's Wi-Fi-sync flag on or off through quince, so setting up
// Wi-Fi backups never needs Finder (qn.7).
//
// ENABLE IS USB-ONLY, and that is a property of the setting rather than a caution. MEASURED
// 2026-07-31: with Wi-Fi sync off the device stops announcing over mDNS entirely — `idevice_id -n`
// returns nothing the moment the flag flips. So a device that NEEDS this turned on is, by
// construction, not reachable over Wi-Fi. The button is disabled-with-a-reason rather than offered
// and then failed, the same shape qn.6a uses for an offline device's "Back up now".
//
// DISABLE IS CONFIRMED, because over Wi-Fi it cuts the connection it is running on: the device
// drops off the page mid-action and can only be brought back with a cable. A warning sentence was
// not enough — the Operator hit exactly that on hardware. Confirmation is scoped to the case that
// is actually destructive; over USB nothing is severed and the click stands on its own.
export function WifiSyncControl({ device, post }: { device: Device; post?: StartFn }) {
  const { op, starting, startError, start, inFlight } = useDeviceOp(post);
  const [confirming, setConfirming] = React.useState(false);

  // "unknown" means quince has not read the flag — never guess a direction from it.
  if (device.wifi_sync === "unknown") return null;

  const on = device.wifi_sync === "on";
  const onUSB = device.transports.usb != null;
  const needsUSBToEnable = !on && !onUSB;
  const willDisconnect = on && !onUSB;

  const run = () => {
    setConfirming(false);
    void start(`/api/devices/${device.udid}/wifi-sync`, { action: on ? "disable" : "enable" });
  };

  // `items-start` is structural, not cosmetic: without it the explanatory paragraph sets this flex
  // column's width and the button stretches to match — which made a once-per-device settings action
  // render wider and louder than "Back up now", the page's primary action.
  return (
    <div className="flex flex-col items-start gap-1">
      <Button
        variant="ghost"
        size="sm"
        onClick={() => (willDisconnect ? setConfirming(true) : run())}
        disabled={inFlight || needsUSBToEnable}
      >
        {on ? <WifiOff size={14} /> : <Wifi size={14} />}
        {on ? "Turn off Wi-Fi sync" : "Turn on Wi-Fi sync"}
      </Button>

      {/* Rendered text, never a `title`. A tooltip does not exist on touch, and ui.design.md makes
          the iPhone a first-class client — on a phone the user would tap, watch the device vanish,
          and never have seen the sentence explaining it. */}
      {needsUSBToEnable ? (
        <p className="max-w-xs text-xs text-muted">
          Connect by cable to turn this on — with Wi-Fi sync off the device does not announce itself
          over Wi-Fi.
        </p>
      ) : null}

      {willDisconnect ? (
        <p className="max-w-xs text-xs text-muted">
          Turning this off will disconnect the device — it reappears when you plug it in.
        </p>
      ) : null}

      <OpNarration op={op} starting={starting} startError={startError} />

      <Dialog open={confirming} onOpenChange={setConfirming}>
        <DialogContent>
          <DialogTitle>Turn off Wi-Fi sync?</DialogTitle>
          <DialogDescription>
            This device is connected over Wi-Fi, so turning Wi-Fi sync off will disconnect it
            immediately. It will not appear in quince again until you plug it in with a cable — and
            turning Wi-Fi sync back on can only be done over that cable.
          </DialogDescription>
          <div className="mt-5 flex justify-end gap-2">
            <Button variant="outline" onClick={() => setConfirming(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={run}>
              Turn off Wi-Fi sync
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
