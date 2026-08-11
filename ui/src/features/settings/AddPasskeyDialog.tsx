import * as React from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { api, APIError } from "@/lib/api";
import { b64urlToBytes, bytesToB64url } from "@/lib/webauthn";

// Naming a passkey, in quince's own dialog — Operator-reported: `window.prompt()` is a native
// browser sheet in an app that has one of these, and it looked nothing like the rest.
//
// IT ALSO FIXES A REAL FAILURE, and that is the more important half. `navigator.credentials.create()`
// requires a LIVE USER ACTIVATION, and Safari does not keep one across an await. The previous flow
// was: click → `window.prompt()` → `await` the begin request → `create()`. By the time `create()`
// ran the activation was gone, Safari threw `NotAllowedError`, and the catch swallowed it as "the
// user dismissed the sheet" — so pressing the button produced NOTHING AT ALL.
//
// So the options are fetched WHEN THE DIALOG OPENS, and `create()` is called directly from the
// Continue button's own handler with no await in front of it.

type BeginRegistration = {
  ceremony: string;
  options: { publicKey: PublicKeyCredentialCreationOptions };
};

export function AddPasskeyDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onAdded: () => void;
}) {
  const [name, setName] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  // The begun ceremony, fetched on open so the Continue click has nothing to await before it.
  const [begun, setBegun] = React.useState<BeginRegistration | null>(null);

  React.useEffect(() => {
    if (!open) return;
    setName("");
    setError(null);
    setBegun(null);
    let cancelled = false;
    void (async () => {
      try {
        const b = await api.post<BeginRegistration>("/api/auth/passkeys/register/begin", {});
        if (!cancelled) setBegun(b);
      } catch (err) {
        // A ceremony that cannot even BEGIN is reported here rather than on the button, because
        // the two failures that matter — an unsupported address, a mismatched rpId — each name a
        // domain, and this is where the user is looking.
        if (!cancelled) setError(err instanceof APIError ? err.message : "Could not start setting up a passkey.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!begun || busy) return;
    setError(null);
    setBusy(true);
    try {
      const pk = begun.options.publicKey;
      // NO AWAIT BEFORE THIS LINE. See the file comment: the activation from the click that ran
      // this handler is what permits it, and Safari does not keep one across a round trip.
      const cred = (await navigator.credentials.create({
        publicKey: {
          ...pk,
          challenge: b64urlToBytes(pk.challenge as unknown as string),
          user: { ...pk.user, id: b64urlToBytes(pk.user.id as unknown as string) },
          excludeCredentials: (pk.excludeCredentials ?? []).map((c) => ({
            ...c,
            id: b64urlToBytes(c.id as unknown as string),
          })),
        },
      })) as PublicKeyCredential | null;

      if (!cred) {
        // NEVER SILENT. "I pressed the button and nothing happened" is the report this dialog
        // exists to make impossible; a null credential is rare but it is still an outcome.
        setError("No passkey was added.");
        return;
      }

      const resp = cred.response as AuthenticatorAttestationResponse;
      await api.post(
        `/api/auth/passkeys/register/finish?ceremony=${encodeURIComponent(begun.ceremony)}` +
          `&name=${encodeURIComponent(name.trim())}`,
        {
          id: cred.id,
          rawId: bytesToB64url(cred.rawId),
          type: cred.type,
          response: {
            clientDataJSON: bytesToB64url(resp.clientDataJSON),
            attestationObject: bytesToB64url(resp.attestationObject),
          },
        },
      );
      onAdded();
      onOpenChange(false);
    } catch (err) {
      if (err instanceof APIError) {
        setError(err.message);
      } else if (err instanceof Error && err.name === "NotAllowedError") {
        // CANCELLED AND NOT-PERMITTED ARE THE SAME ERROR, and the browser will not tell us which.
        // So the message says what is TRUE of both rather than guessing at one: nothing was added,
        // and here is the other thing it might have been. Reporting "you cancelled" would be a
        // claim, and this is exactly the case where that claim was wrong.
        setError(
          "No passkey was added — the request was cancelled, or your browser did not allow it. " +
            "Passkeys need a secure connection to a domain name.",
        );
      } else {
        setError(err instanceof Error ? err.message : "Could not add a passkey.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogTitle>Add a passkey</DialogTitle>
        <DialogDescription>
          Give it a name you will recognise later — several devices can each have their own, and this
          is how you tell them apart when you come to remove one.
        </DialogDescription>

        <form onSubmit={submit}>
          <div className="mt-4 flex flex-col gap-1">
            <Label htmlFor="passkey-name">Name</Label>
            <Input
              id="passkey-name"
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my iPhone"
            />
          </div>

          <div className="mt-4 min-h-6">
            {error ? <p className="text-sm text-danger">{error}</p> : null}
          </div>

          <div className="mt-4 flex justify-end gap-2">
            {/* type="button" on everything that is not the submit — components/ui/button.tsx sets
                no type, so a Button inside a form is a submit by default (quince#824, quince#828). */}
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !begun || name.trim() === ""}>
              {busy ? "Working…" : "Continue"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
