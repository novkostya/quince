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
    start(`/api/devices/${device.udid}/wifi-sync`, { action: on ? "disable" : "enable" });

  // `items-start` is structural, not cosmetic: without it the explanatory paragraph below sets this
  // flex column's width and the button stretches to match it — which made a rare settings action
  // render wider and louder than "Back up now", the page's actual primary action.
  //
  // `ghost` + `sm` for the same reason. Turning Wi-Fi sync on happens roughly once per device, so it
  // belongs at tertiary weight beside a primary ("Back up now") and a secondary ("Manage
  // encryption"). It was `outline` + default size, which put it level with the secondary.
  return (
    <div className="flex flex-col items-start gap-1">
      <Button
        variant="ghost"
        size="sm"
        onClick={submit}
        disabled={inFlight || needsUSBToEnable}
      >
        {on ? <WifiOff size={14} /> : <Wifi size={14} />}
        {on ? "Turn off Wi-Fi sync" : "Turn on Wi-Fi sync"}
      </Button>

      {/* ONE line, in the one state where it applies — never a `title`. A tooltip does not exist on
          touch, and `ui.design.md` makes the iPhone a first-class client: on a phone the user would
          tap "Turn off Wi-Fi sync", watch the device vanish from the page, and never have seen the
          sentence explaining it. That is the confusion this text exists to prevent, on the surface
          where it is most likely. The weight problem was the paragraph setting the button's width,
          which `items-start` fixes — not the sentence being present. */}
      {needsUSBToEnable ? (
        <p className="max-w-xs text-xs text-muted">
          Connect by cable to turn this on — with Wi-Fi sync off the device does not announce itself
          over Wi-Fi.
        </p>
      ) : null}

      {on && !onUSB ? (
        <p className="max-w-xs text-xs text-muted">
          Turning this off will disconnect the device — it reappears when you plug it in.
        </p>
      ) : null}

      <OpNarration op={op} starting={starting} startError={startError} />
    </div>
  );
}
