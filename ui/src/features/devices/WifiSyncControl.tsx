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
  // `on USB` and `here at all` are DIFFERENT questions, and answering the second with the first is
  // what put a live "Turn off Wi-Fi sync" on a device that was simply gone — offering a confirmation
  // dialog that opened with "This device is connected over Wi-Fi" about a device connected to
  // nothing (quince#325 (2a), from an Operator screenshot). An absent device is unreachable on
  // either transport, so the op would reach nothing.
  const present = onUSB || device.transports.wifi != null;
  const needsUSBToEnable = !on && !onUSB;
  const willDisconnect = on && !onUSB && present;
  // Disabled-with-a-reason rather than hidden, the same shape DeviceCard uses for "Back up now" on
  // an offline device — the card keeps its layout and the user is told why, instead of the control
  // silently vanishing whenever the phone walks out of range.
  const unreachable = on && !present;

  const run = () => {
    setConfirming(false);
    void start(`/api/devices/${device.udid}/wifi-sync`, { action: on ? "disable" : "enable" });
  };

  // `items-start` is structural, not cosmetic: without it the explanatory paragraph sets this flex
  // column's width and the button stretches to match — which made a once-per-device settings action
  // render wider and louder than "Back up now", the page's primary action.
  return (
    <div className="flex flex-col items-start gap-1">
      {/* WEIGHT FOLLOWS DIRECTION, because the two directions are not the same kind of action
          (quince#352). Turning Wi-Fi sync ON is the onboarding step this whole rung exists for —
          once per device, and the thing standing between a user and cable-free backups, so it earns
          a real button. Turning it OFF is a setting nobody reaches for, so it recedes to `ghost`.
          Same control, opposite prominence; the alternative was one weight that is wrong half the
          time. The full answer is #352 (disable does not belong on this page at all) and this is
          the part that does not need a new home first. */}
      <Button
        variant={on ? "ghost" : "outline"}
        size={on ? "sm" : "md"}
        // A ghost button has no background, so its TEXT sits at the size's px-3 inset while every
        // neighbour's visible left edge — the filled "Back up now", the status line beneath it — is
        // at the margin. It reads as a stray indent rather than as a quieter control. `-ml-3`
        // cancels that padding so the text starts where the column starts, and the hover target
        // keeps its full width. Not needed for `outline`, which has a border at the margin already.
        className={on ? "-ml-3" : undefined}
        onClick={() => (willDisconnect ? setConfirming(true) : run())}
        disabled={inFlight || needsUSBToEnable || unreachable}
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
      {unreachable ? (
        <p className="max-w-xs text-xs text-muted">Connect the device to turn this off.</p>
      ) : null}

      {/* The disconnect consequence is NOT standing text — it is the confirmation dialog's whole
          content, which is where it belongs: a warning about a click arrives at the moment of the
          click, not as ambient prose above it. That also answers the earlier review correctly: the
          objection was to a `title`, which does not exist on touch. A dialog does. */}

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
