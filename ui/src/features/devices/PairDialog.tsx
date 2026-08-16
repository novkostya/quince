import * as React from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { useDeviceOp, type StartFn } from "./useDeviceOp";
import { OpNarration } from "./OpNarration";
import { useDialogRoute } from "@/lib/useDialogRoute";
import { useDevicesStore } from "@/stores/devices";

// PairDialog drives POST /api/devices/{udid}/pair and narrates the assisted flow (tap Trust +
// passcode) from the op.updated stream. `post` is injectable for tests. `autoOpen` opens the dialog
// on arrival — the dashboard card's Pair deep-links a pair intent so the click lands IN the dialog
// (qn.4b fix for (bq); qn.3's decision that the narrated flow lives on details stands).
export function PairDialog({ udid, post, autoOpen }: { udid: string; post?: StartFn; autoOpen?: boolean }) {
  // Open-ness lives in the URL (quince#931): Back closes the dialog, and the offset of the page
  // behind it is restored by the browser rather than left where the keyboard put it.
  const { open, onOpenChange: setOpen } = useDialogRoute("pair");
  const pairing = useDevicesStore((s) => s.pairing);
  const { op, starting, startError, start, reset, inFlight } = useDeviceOp(post);
  const done = op?.state === "succeeded";

  // A pair intent carried in from the dashboard card auto-opens the dialog on arrival. It goes
  // through the same push as a tap would, so Back from an auto-opened dialog lands on the device
  // page rather than back at the card that sent you here.
  React.useEffect(() => {
    if (autoOpen) setOpen(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- an intent fires once, on arrival
  }, [autoOpen]);

  // A completed pairing closes the dialog after a brief confirmation (the device transitions to
  // its paired state in the page behind it).
  React.useEffect(() => {
    if (op?.state !== "succeeded") return;
    const t = window.setTimeout(() => {
      setOpen(false);
      reset();
    }, 1000);
    return () => window.clearTimeout(t);
  }, [op?.state, reset]);

  function change(o: boolean) {
    setOpen(o);
    if (!o) reset();
  }

  // Pairing records live in one directory, and if quince cannot write there a pairing would not
  // survive being made (qn.6p D7). The control stays VISIBLE and disabled with the reason beside
  // it rather than disappearing: a missing button explains nothing, and this state is usually
  // deliberate — another tool owns the records and quince mounts them read-only.
  if (!pairing.writable) {
    return (
      <div className="space-y-1">
        <Button disabled>Pair</Button>
        <p className="text-sm text-muted">
          quince can’t save pairing records here, so it can’t pair this device. Existing pairings
          still work — a device paired elsewhere is still listed and still backs up.
        </p>
        {pairing.reason ? (
          // The server's words, kept SECONDARY: they carry a path and an errno, which is what an
          // operator needs in order to fix it and not what a user should have to read in order to
          // understand what is wrong.
          <p className="text-xs text-subtle">{pairing.reason}</p>
        ) : null}
      </div>
    );
  }

  return (
    <>
      <Button onClick={() => setOpen(true)}>Pair</Button>
      <Dialog open={open} onOpenChange={change}>
        <DialogContent>
          <DialogTitle>Pair this device</DialogTitle>
          <DialogDescription>
            Approve the connection on the device — tap <strong>Trust</strong>, then enter its
            passcode. Pairing needs a USB connection.
          </DialogDescription>
          <div className="mt-4 min-h-6">
            <OpNarration op={op} starting={starting} startError={startError} />
          </div>
          <div className="mt-6 flex justify-end gap-2">
            {done ? (
              <Button onClick={() => change(false)}>Done</Button>
            ) : (
              <>
                <Button variant="outline" onClick={() => change(false)}>
                  Cancel
                </Button>
                <Button onClick={() => start(`/api/devices/${udid}/pair`)} disabled={inFlight}>
                  {inFlight ? "Pairing…" : "Start pairing"}
                </Button>
              </>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
