import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { useDeviceOp, type StartFn } from "./useDeviceOp";
import { OpNarration } from "./OpNarration";

export type EncryptionMode = "enable" | "change" | "disable";

// EncryptionDialog manages the device backup password (contracts §1 POST .../encryption).
// Controlled by the parent so both the "Manage encryption" button and the unencrypted-device
// banner's CTA can open it. Passwords live only in local state and the POST body — never a URL,
// never logged; their onward handling (pty, never argv) is the core's job (story 5).
export function EncryptionDialog({
  udid,
  encryption,
  open,
  onOpenChange,
  initialMode,
  post,
}: {
  udid: string;
  encryption: "on" | "off" | "unknown";
  open: boolean;
  onOpenChange: (o: boolean) => void;
  initialMode?: EncryptionMode;
  post?: StartFn;
}) {
  const canManage = encryption !== "off"; // change/disable need existing encryption
  const [mode, setMode] = React.useState<EncryptionMode>(
    initialMode ?? (encryption === "off" ? "enable" : "change"),
  );
  const [currentPw, setCurrentPw] = React.useState("");
  const [newPw, setNewPw] = React.useState("");
  const [confirmPw, setConfirmPw] = React.useState("");
  const [formError, setFormError] = React.useState<string | null>(null);
  const { op, starting, startError, start, reset, inFlight } = useDeviceOp(post);
  const done = op?.state === "succeeded";

  const clearFields = React.useCallback(() => {
    setCurrentPw("");
    setNewPw("");
    setConfirmPw("");
    setFormError(null);
  }, []);

  // Pick the mode + clear prior state only on the OPEN transition — NOT on every encryption
  // change. Otherwise a successful op flips the device's encryption, which re-derives the mode
  // and leaves the title mismatched with the result just shown ("Enable…" over "…is off").
  const prevOpen = React.useRef(false);
  React.useEffect(() => {
    if (open && !prevOpen.current) {
      setMode(initialMode ?? (encryption === "off" ? "enable" : "change"));
      reset();
      clearFields();
    }
    prevOpen.current = open;
  }, [open, initialMode, encryption, reset, clearFields]);

  // A completed op closes the dialog (after a brief confirmation) rather than lingering in a
  // recomputed state; the device card/badge reflects the new state.
  React.useEffect(() => {
    if (op?.state !== "succeeded") return;
    const t = window.setTimeout(() => onOpenChange(false), 1000);
    return () => window.clearTimeout(t);
  }, [op?.state, onOpenChange]);

  function change(o: boolean) {
    onOpenChange(o);
  }

  // Switching mode (incl. after a success) resets the previous op so the form returns for the
  // new action — otherwise the switcher looks dead once an op has completed.
  function switchMode(m: EncryptionMode) {
    setMode(m);
    reset();
    clearFields();
  }

  function submit() {
    setFormError(null);
    if (mode === "enable") {
      if (!newPw) return setFormError("Enter a password.");
      if (newPw !== confirmPw) return setFormError("Passwords don't match.");
      void start(`/api/devices/${udid}/encryption`, { action: "enable", password: newPw });
    } else if (mode === "change") {
      if (!currentPw || !newPw) return setFormError("Enter the current and new passwords.");
      if (newPw !== confirmPw) return setFormError("New passwords don't match.");
      void start(`/api/devices/${udid}/encryption`, {
        action: "change_password",
        old_password: currentPw,
        new_password: newPw,
      });
    } else {
      if (!currentPw) return setFormError("Enter the current password.");
      void start(`/api/devices/${udid}/encryption`, { action: "disable", password: currentPw });
    }
  }

  const title =
    mode === "enable"
      ? "Enable backup encryption"
      : mode === "change"
        ? "Change backup password"
        : "Disable backup encryption";

  return (
    <Dialog open={open} onOpenChange={change}>
      <DialogContent>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>
          This is the device&rsquo;s backup password — the same one that later unlocks its
          backups. quince sets it and never stores it.
        </DialogDescription>

        {canManage ? (
          <div className="mt-4 flex gap-2">
            <Button
              size="sm"
              variant={mode === "change" ? "accent" : "outline"}
              onClick={() => switchMode("change")}
            >
              Change password
            </Button>
            <Button
              size="sm"
              variant={mode === "disable" ? "destructive" : "outline"}
              onClick={() => switchMode("disable")}
            >
              Disable
            </Button>
          </div>
        ) : null}

        {!done ? (
          <div className="mt-4 flex flex-col gap-3">
            {(mode === "change" || mode === "disable") && (
              <div className="flex flex-col gap-1">
                <Label htmlFor="enc-current">Current password</Label>
                <Input
                  id="enc-current"
                  type="password"
                  autoComplete="current-password"
                  value={currentPw}
                  onChange={(e) => setCurrentPw(e.target.value)}
                />
              </div>
            )}
            {(mode === "enable" || mode === "change") && (
              <>
                <div className="flex flex-col gap-1">
                  <Label htmlFor="enc-new">New password</Label>
                  <Input
                    id="enc-new"
                    type="password"
                    autoComplete="new-password"
                    value={newPw}
                    onChange={(e) => setNewPw(e.target.value)}
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <Label htmlFor="enc-confirm">Confirm new password</Label>
                  <Input
                    id="enc-confirm"
                    type="password"
                    autoComplete="new-password"
                    value={confirmPw}
                    onChange={(e) => setConfirmPw(e.target.value)}
                  />
                </div>
              </>
            )}
            {mode === "disable" ? (
              <p className="text-sm text-warn">
                Disabling encryption is discouraged: Health, Keychain, saved passwords, and call
                history are omitted from unencrypted backups.
              </p>
            ) : null}
          </div>
        ) : null}

        {!done ? (
          <p className="mt-3 text-xs text-muted">
            Keep the device unlocked — it will ask you to confirm this change with its passcode.
          </p>
        ) : null}

        <div className="mt-4 min-h-6">
          {formError ? <p className="text-sm text-danger">{formError}</p> : null}
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
              <Button
                variant={mode === "disable" ? "destructive" : "accent"}
                onClick={submit}
                disabled={inFlight}
              >
                {inFlight ? "Working…" : title}
              </Button>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
