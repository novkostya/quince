import { useState } from "react";
import { CheckboxRow } from "@/components/ui/checkbox-row";
import type { Device } from "@/lib/types";
import { api } from "@/lib/api";
import { useDevicesStore } from "@/stores/devices";

// DeviceNotificationsControl silences notifications about ONE device (quince#1270).
//
// THE CASE IT EXISTS FOR IS A PHONE IN A DRAWER. `qn.12`'s category switches are global, so a paired
// device nobody intends to back up produces "Never Backed Up" every `reminder_cooldown_hours`
// indefinitely — and the only remedy was to turn `backup_available` off, which silences the
// invitation for the devices you DO want backed up.
//
// THE LABEL NAMES THE SUBJECT DEVICE AND NEVER SAYS "THIS DEVICE". That is the one hard constraint
// on the copy here, and it is not stylistic. `NotificationsThisDevice.test.tsx` records what the
// collision already cost once: the install page asked *"is ANY subscription live"*, so a second
// browser read another's subscription as its own — its heading is "THIS DEVICE MUST MEAN THIS
// DEVICE". quince's notification settings already say "this device" about the SUBSCRIBER axis (which
// browser receives). This switch is the SUBJECT axis (which device generates), it lives on a page
// about one device, and a label reading "this device" here would be that same collision one level
// over.
//
// THE NAME FALLS BACK EXACTLY AS THE NOTIFICATION'S WOULD — name, then model, then a generic. That
// is `notify.deviceName`'s chain, and matching it means the switch is labelled with the words the
// push itself will use. A UDID is never shown: it is Operator-private and meaningless to a reader.
// NotificationsResponse is `PUT /api/devices/{udid}/notifications`'s 200 body (contracts §1).
//
// `enabled` IS WHAT QUINCE RECORDED, not what the request carried — true by construction since
// quince#1292, which is why this component reads it instead of its own argument.
interface NotificationsResponse {
  enabled: boolean;
}

function subjectName(device: Device): string {
  return device.name || device.model || "this iPhone or iPad";
}

// GLOBAL ACROSS SUBSCRIBERS, and the sentence says so rather than leaving it to be discovered. The
// gate runs in `notify.Evaluate`, BEFORE subscriber fan-out, so turning this off silences the device
// on every browser you have subscribed — not only the one you flipped it from.
export function DeviceNotificationsControl({
  device,
  put,
}: {
  device: Device;
  // TYPED AS THE CONTRACT, NOT AS `unknown`. The response body is what this component reads,
  // so the seam a test injects has to promise the same shape the API does — otherwise the
  // production path and the test path differ in the one thing under test.
  put?: (path: string, body: unknown) => Promise<NotificationsResponse>;
}) {
  const upsert = useDevicesStore((s) => s.upsert);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const send =
    put ?? ((path: string, body: unknown) => api.put<NotificationsResponse>(path, body));

  const onChange = async (enabled: boolean) => {
    setSaving(true);
    setError(null);
    try {
      const res = await send(`/api/devices/${device.udid}/notifications`, { enabled });
      // THE STORE TAKES THE RESPONSE'S VALUE, NOT THE ONE THIS COMPONENT ASKED FOR.
      //
      // `PUT …/notifications` returns what quince RECORDED, by construction rather than by
      // coincidence (quince#1292): the handler echoes what the write reported storing, resolved
      // through the same registry read that publishes `device.updated`. This is that
      // guarantee's client, and the whole point of it is that a client never has to ASSUME its
      // own request succeeded in order to render the control it just moved. Reading `enabled`
      // here instead would be that assumption, correct today and silently wrong the moment
      // storage gains a policy that refuses or coerces.
      //
      // Not optimistic, either — this runs after the write. The daemon also
      // publishes `device.updated`, which arrives with the same value and is idempotent — this is
      // what makes the control move on a page whose socket is down, rather than sitting on the old
      // value until somebody refreshes.
      upsert({ ...device, notifications_enabled: res.enabled });
    } catch (e) {
      // THE CHECKBOX DOES NOT MOVE ON A FAILED WRITE, because it renders the stored value and the
      // stored value did not change. What the user gets is the reason, which is the difference
      // between "quince refused" and "quince silently did nothing".
      setError(e instanceof Error ? e.message : "the setting could not be saved");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col items-start gap-1">
      <CheckboxRow
        checked={device.notifications_enabled}
        disabled={saving}
        onChange={(e) => void onChange(e.target.checked)}
      >
        <span>Notify me about {subjectName(device)}</span>
      </CheckboxRow>
      {!device.notifications_enabled ? (
        <p className="max-w-md text-xs text-muted">
          quince will not send any notification about {subjectName(device)} — not a reminder,
          and not a failure. That applies to every browser you have subscribed. No other
          device is affected.
        </p>
      ) : null}
      {error ? <p className="max-w-md text-xs text-danger">{error}</p> : null}
    </div>
  );
}
