import * as React from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { useDeviceOp, type StartFn } from "./useDeviceOp";
import { OpNarration } from "./OpNarration";

// PairDialog drives POST /api/devices/{udid}/pair and narrates the assisted flow (tap Trust +
// passcode) from the op.updated stream. `post` is injectable for tests.
export function PairDialog({ udid, post }: { udid: string; post?: StartFn }) {
  const [open, setOpen] = React.useState(false);
  const { op, starting, startError, start, reset, inFlight } = useDeviceOp(post);
  const done = op?.state === "succeeded";

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
