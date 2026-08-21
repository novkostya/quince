import { useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { api, messageFor } from "@/lib/api";
import type { Device } from "@/lib/types";

// DeviceEnrolment — the admin issues, sees and cancels the QR codes that give a household member
// access to ONE device (qn.13 D4, D5, D9; slice 9d-2).
//
// THE QR IS A CREDENTIAL-ISSUING TOKEN, and the screen says so. Whoever scans it gets what it
// grants, so the copy names the window rather than leaving the reader to assume a code on a screen
// is inert.

interface Enrolment {
  id: string;
  udid: string;
  created_at: string;
  expires_at: string;
}

interface Issued extends Enrolment {
  secret: string;
}

// enrolURL builds what the QR encodes, FROM THIS BROWSER'S OWN ADDRESS.
//
// THE SERVER NEVER GUESSES ONE, and that is D5 rather than a convenience. This single URL fixes
// three things at once — the passkey's rpId, the push origin, and the Home Screen web clip frozen at
// install — and two of those fail SILENTLY when wrong. quince may sit behind a proxy that strips or
// misreports the address it was reached at; the browser showing this page cannot be wrong about it.
function enrolURL(secret: string): string {
  return `${window.location.origin}/enrol?secret=${encodeURIComponent(secret)}`;
}

export function DeviceEnrolment({ device }: { device: Device }) {
  const qc = useQueryClient();
  const [issued, setIssued] = useState<Issued | null>(null);
  const key = ["enrolments", device.udid];

  const list = useQuery({
    queryKey: key,
    queryFn: () => api.get<{ enrolments: Enrolment[] }>(`/api/devices/${device.udid}/enrolments`),
  });

  const create = useMutation({
    mutationFn: () => api.post<Issued>(`/api/devices/${device.udid}/enrolments`, {}),
    onSuccess: (e) => {
      setIssued(e);
      void qc.invalidateQueries({ queryKey: key });
    },
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api.del(`/api/devices/${device.udid}/enrolments/${id}`),
    onSuccess: (_res, id) => {
      // THE SHOWN CODE DISAPPEARS WITH ITS ROW. Leaving a cancelled QR on screen would be a live
      // credential that is not one — the worst direction for this particular thing to be wrong in.
      if (issued?.id === id) setIssued(null);
      void qc.invalidateQueries({ queryKey: key });
    },
  });

  const outstanding = list.data?.enrolments ?? [];

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted">
        Give someone access to this {device.name || "device"} and nothing else on this quince. They
        scan a code, add a passkey, and see only this one device.
      </p>

      {issued && (
        <div className="flex flex-col items-start gap-3 rounded-lg border border-line p-4">
          {/* WHITE BEHIND THE CODE, ALWAYS. A QR rendered on a dark surface with a dark foreground
              is unscannable, and this app has a dark theme — so the quiet zone is a fixed white
              box rather than a themed one. */}
          <div className="rounded bg-white p-3">
            <QRCodeSVG value={enrolURL(issued.secret)} size={168} />
          </div>
          <div className="text-sm">
            <p className="font-medium">Scan this with the phone or iPad you are handing over.</p>
            {/* D5, ON SCREEN. The address is named rather than assumed, because the code carries
                whatever address this page was opened at — and if the phone reaches quince by a
                different one, the passkey, the notifications and the home-screen icon all break,
                two of them without saying anything. */}
            <p className="mt-1 text-muted">
              It points at <strong>{window.location.origin}</strong>. The device you hand this to
              must be able to reach quince at that exact address — if it normally uses a different
              one, generate the code from there instead.
            </p>
            <p className="mt-1 text-muted">
              It works once, and only for a few minutes. Cancel it below if you change your mind.
            </p>
          </div>
        </div>
      )}

      {create.isError && (
        <p role="alert" className="text-sm text-danger">
          {messageFor(create.error, "Could not create a code.")}
        </p>
      )}
      {revoke.isError && (
        <p role="alert" className="text-sm text-danger">
          {messageFor(revoke.error, "Could not cancel that code.")}
        </p>
      )}

      <div>
        <Button onClick={() => create.mutate()} disabled={create.isPending}>
          {create.isPending ? "Creating…" : "Create a code"}
        </Button>
      </div>

      {/* OUTSTANDING CODES ARE LISTED EVEN THOUGH THE SECRET IS GONE FROM THEM. An issued and
          unscanned QR is live authority, and *authority nobody can see is authority nobody
          revokes* — the ruling named this as the part not to trade away. The value is not
          re-displayable; what the admin needs here is to know one exists and to be able to stop
          it. */}
      {outstanding.length > 0 && (
        <div className="flex flex-col gap-2">
          <p className="text-sm font-medium">Waiting to be used</p>
          <ul className="flex flex-col gap-2">
            {outstanding.map((e) => (
              <li key={e.id} className="flex items-center justify-between gap-3 text-sm">
                <span className="text-muted">
                  Created {new Date(e.created_at).toLocaleTimeString()}, expires{" "}
                  {new Date(e.expires_at).toLocaleTimeString()}
                </span>
                <Button
                  variant="ghost"
                  onClick={() => revoke.mutate(e.id)}
                  disabled={revoke.isPending}
                >
                  Cancel
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
