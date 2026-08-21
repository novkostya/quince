import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { api, messageFor } from "@/lib/api";
import type { Session, Version } from "@/lib/types";

// UnlockDialog opens one version for browsing (contracts §1, POST /api/versions/{id}/unlock).
//
// THE PASSWORD IS THE BACKUP'S, NOT QUINCE'S, and it is the SAME SECRET the encryption dialog
// sets. That is why the credential anchor below is `deviceName || udid`, character for
// character what `EncryptionDialog` uses: the browser files saved passwords under
// (origin, username), so anchoring these two screens differently would file two entries for
// one secret. Nothing would fail — encryption would keep working, unlock would keep working,
// every test would stay green — and the password the user saved when they turned encryption
// on would simply never be offered on the screen that asks for it. There is no error state
// for that, which is why it is written here rather than left to be noticed.
//
// AN UNENCRYPTED VERSION IS NOT OFFERED A PASSWORD FIELD AT ALL (spec D7). It needs none, and
// a field that accepts any string and validates nothing is worse than no field — the seam
// ignores the argument and reports the case through Info.Encrypted instead.
export function UnlockDialog({
  version,
  deviceName,
  open,
  onOpenChange,
  onUnlocked,
}: {
  version: Version;
  // RAW, not pre-resolved: this component owns the `name || udid` fallback so a second call
  // site cannot mint a different anchor for the same credential.
  deviceName?: string;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onUnlocked: (s: Session) => void;
}) {
  const credentialName = deviceName || version.udid;
  const [password, setPassword] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  // Clear on the OPEN transition only, so a failed attempt's message survives a re-render but
  // a fresh open never shows the last one.
  const prevOpen = React.useRef(false);
  React.useEffect(() => {
    if (open && !prevOpen.current) {
      setPassword("");
      setError(null);
      setBusy(false);
    }
    prevOpen.current = open;
  }, [open]);

  async function submit(e: React.FormEvent) {
    // A REAL <form> WITH A REAL submit HANDLER. The browser's save prompt depends on the
    // password field sitting in a form that emits `submit` — see `passwordSurfaces.test.tsx`,
    // which asserts that across every password screen because one of them once drifted alone.
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const s = await api.post<Session>(`/api/versions/${version.id}/unlock`, { password });
      onUnlocked(s);
      onOpenChange(false);
    } catch (err) {
      // The server's message is shown VERBATIM rather than replaced with a generic one: the
      // vault surface answers with a reason and a remedy (contracts §4), and rewriting it here
      // would collapse causes the user needs to tell apart — a wrong backup password and a
      // version whose class this build cannot open want different actions.
      setError(messageFor(err, "Could not unlock this backup."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogTitle>Browse this backup</DialogTitle>
        <DialogDescription>
          {version.encrypted
            ? "Enter the backup password you set for this device. quince uses it only to open this backup, and never stores it."
            : "This backup is not encrypted, so it needs no password."}
        </DialogDescription>

        <form onSubmit={submit} className="flex flex-col gap-4">
          {/* The anchor is rendered for BOTH cases: it is a property of the credential rather
              than of this action, and an unencrypted version still belongs to a device the
              browser may hold a password for. readOnly and visible rather than suppressed —
              see PasswordForm for why, and tabIndex={-1} so the first Tab lands on something
              the user can actually edit. */}
          <div className="flex flex-col gap-1">
            <Label htmlFor="unlock-device">Device</Label>
            <Input
              id="unlock-device"
              name="username"
              type="text"
              autoComplete="username"
              readOnly
              tabIndex={-1}
              value={credentialName}
            />
          </div>

          {version.encrypted ? (
            <div className="flex flex-col gap-1">
              <Label htmlFor="unlock-password">Backup password</Label>
              <Input
                id="unlock-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
          ) : null}

          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? "Opening…" : "Open"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
