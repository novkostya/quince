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
  deviceName,
  encryption,
  open,
  onOpenChange,
  initialMode,
  post,
}: {
  udid: string;
  // The device's own name, for the credential anchor below. RAW, not pre-resolved: the dialog owns
  // the `name || udid` fallback so a second call site cannot mint a different rule for it (quince#819).
  deviceName?: string;
  encryption: "on" | "off" | "unknown";
  open: boolean;
  onOpenChange: (o: boolean) => void;
  initialMode?: EncryptionMode;
  post?: StartFn;
}) {
  const canManage = encryption !== "off"; // change/disable need existing encryption
  // `name || udid` is this codebase's existing idiom for naming a device — DeviceCard,
  // DeviceDetailsPage and StorageDetailsPage all carry it verbatim — so an unnamed device reads the
  // same here as everywhere else rather than getting a second rule (quince#819).
  const credentialName = deviceName || udid;
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

  function submit(e: React.FormEvent) {
    e.preventDefault();
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

        {/* A REAL FORM, BECAUSE A SAVE PROMPT IS DRIVEN BY SUBMISSION — quince#819. These password
            inputs sat in a plain `div` with an onClick button, so a password manager had only its
            weaker heuristic to go on and offered to save late and out of context. `PasswordForm` was
            already a form; this was the surface that was not.

            EVERY `Button` INSIDE THIS FORM NEEDS AN EXPLICIT `type`, and there are five of them.
            `ui/components/ui/button.tsx` sets none, so a bare `<button>` in a form is `type="submit"`
            by the HTML default — which means wrapping this dialog silently converted the two mode
            switchers below AND `Cancel` and `Done` in the footer into submits. quince#819 named the
            two switchers; `Cancel` is the one that would have been felt, since dismissing the dialog
            would have posted the form instead.

            FIXED HERE AT THE CALL SITES RATHER THAN AT THE SHARED `Button`, deliberately: defaulting
            `Button` to `type="button"` changes behaviour at every call site in the product, which is
            its own claim with its own blast radius and is unbundled to its own issue (architect
            ruling on quince#819). It is the better fix and is not being rejected — only reviewed
            separately. Until it lands, the next form to wrap a `Button` rediscovers this. */}
        <form onSubmit={submit}>
          {canManage ? (
            <div className="mt-4 flex gap-2">
              <Button
                type="button"
                size="sm"
                variant={mode === "change" ? "accent" : "outline"}
                onClick={() => switchMode("change")}
              >
                Change password
              </Button>
              <Button
                type="button"
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
              {/* WHICH CREDENTIAL THIS IS — quince#819. Without it this dialog and the admin login
                  form are two origin-only entries claiming to be the same password, so iCloud
                  Keychain files them together and offers the wrong one. The device's name is what
                  makes the saved entry readable in the Passwords app AND what the user recognises;
                  the UDID is the fallback rather than the value, because a UDID in a screenshot of
                  a settings screen is how identifiers escape. Rendered in every mode, since the
                  anchor is a property of the credential rather than of the action.

                  See `PasswordForm` for why this is `readOnly` and visible rather than suppressed. */}
              <div className="flex flex-col gap-1">
                <Label htmlFor="enc-device">Device</Label>
                <Input
                  id="enc-device"
                  name="username"
                  type="text"
                  autoComplete="username"
                  readOnly
                  value={credentialName}
                />
              </div>
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
              <Button type="button" onClick={() => change(false)}>
                Done
              </Button>
            ) : (
              <>
                <Button type="button" variant="outline" onClick={() => change(false)}>
                  Cancel
                </Button>
                {/* The ONE submit in this form. Its `onClick` is gone: the click reaches `submit`
                    through the form's own submission, which is what makes Enter-in-a-password-field
                    work and what a password manager watches for. */}
                <Button
                  type="submit"
                  variant={mode === "disable" ? "destructive" : "accent"}
                  disabled={inFlight}
                >
                  {inFlight ? "Working…" : title}
                </Button>
              </>
            )}
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
