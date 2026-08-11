import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { APIError } from "@/lib/api";
import { changePassword, removePassword } from "@/lib/auth";
import { passkeysKey, usePasskeyList } from "@/features/settings/Passkeys";

// The password controls on `/settings/auth` — qn.6m slice 6b, D4 and D7.
//
// TWO ACTIONS, NOT ONE FORM. Changing the password and removing it are different decisions with
// different consequences, and the second is the one quince#841 attached a cost to. Putting them in
// one form would put a destructive option one mis-click from an ordinary one.

// WHAT PASSWORDLESS ACTUALLY COSTS, ON THE SCREEN THAT OFFERS IT — D7, and quince#841 is explicit:
// "it should be said on the screen that offers passwordless, not only in docs". Both sentences are
// measured facts about this build rather than general warnings:
//
//   - `quince auth reset` clears the password, EVERY passkey and EVERY session (auth/reset.go), so
//     it is the only way back in and it takes everything with it;
//   - it runs on the host, so it needs console or SSH access. A headless or remote install with
//     neither is unrecoverable.
//
// The second is the one a user cannot discover for themselves, and it is the one that decides
// whether this option is safe for THEIR deployment.
function PasswordlessCost() {
  return (
    <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-muted">
      <li>
        Your passkey becomes the only way in. If you lose the device holding it, the only way back is{" "}
        <code className="font-mono text-fg">quince auth reset</code> on the machine running quince.
      </li>
      <li>
        That command clears the password, <span className="font-medium">every passkey</span> and every
        signed-in session — it is a fresh start, not a repair.
      </li>
      <li>
        It needs console or SSH access to that machine.{" "}
        <span className="font-medium">
          If you cannot get a shell on it, there is no way back in at all.
        </span>
      </li>
    </ul>
  );
}

export function PasswordControls() {
  const qc = useQueryClient();
  // quince#855 — WITHOUT THIS THE SCREEN LIED QUIETLY. It said "Change your password / Current
  // password" on a passwordless install, where the field had to be left blank and nothing said so.
  // `PUT /api/auth/password` already handled that case correctly, so the defect was entirely in what
  // this surface CLAIMED — the state-honesty rule applied to a form's own labels.
  //
  // UNDEFINED WHILE LOADING, AND THE FALLBACK IS `true` (a password exists). That is the shape the
  // overwhelming majority of installs have, and it is the SAFE guess of the two: showing a Current
  // field that turns out to be unnecessary costs one ignored input, where hiding one that IS
  // required costs a 401 the user cannot act on.
  const list = usePasskeyList();
  const hasPassword = list.data?.has_password ?? true;
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [changeBusy, setChangeBusy] = useState(false);
  const [changeMsg, setChangeMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const [confirming, setConfirming] = useState(false);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeErr, setRemoveErr] = useState<string | null>(null);

  async function submitChange(e: React.FormEvent) {
    e.preventDefault();
    setChangeBusy(true);
    setChangeMsg(null);
    try {
      await changePassword(current, next);
      setCurrent("");
      setNext("");
      // A SUCCESS MESSAGE, because nothing else on screen changes. A password change that looks
      // like nothing happened invites a second attempt with the OLD current password, which then
      // 401s and reads as "the change failed" — the opposite of the truth.
      setChangeMsg({
        ok: true,
        text: hasPassword
          ? "Password changed. Your other devices stay signed in."
          : "Password set. You can now sign in with it as well as your passkey.",
      });
      // The list carries `has_password`, and SETTING one changes it — so the surface that just
      // said "Set a password" must stop saying it. Invalidated for the same reason removal is.
      await qc.invalidateQueries({ queryKey: passkeysKey });
    } catch (err) {
      setChangeMsg({ ok: false, text: messageFor(err, "Could not change the password.") });
    } finally {
      setChangeBusy(false);
    }
  }

  async function submitRemove() {
    setRemoveBusy(true);
    setRemoveErr(null);
    try {
      await removePassword();
      // The passkey list is what decides whether this was allowed, and going passwordless changes
      // what that surface should say about itself — so it is refetched rather than left stale.
      await qc.invalidateQueries({ queryKey: passkeysKey });
      setConfirming(false);
    } catch (err) {
      setRemoveErr(messageFor(err, "Could not remove the password."));
    } finally {
      setRemoveBusy(false);
    }
  }

  return (
    <div className="mt-8 space-y-8">
      <section>
        {/* THE HEADING IS THE ACTION, and on a passwordless install the action is SET, not change.
            Same form, same endpoint — `PUT /api/auth/password` treats an absent current password as
            "there is none" — so only the words and the one field differ. */}
        <h2 className="text-sm font-semibold">
          {hasPassword ? "Change your password" : "Set a password"}
        </h2>
        {!hasPassword ? (
          <p className="mt-1 max-w-xl text-sm text-muted">
            This quince has no password — you sign in with a passkey. Adding one gives you a second
            way in that does not depend on a device.
          </p>
        ) : null}
        <form onSubmit={submitChange} className="mt-3 max-w-sm space-y-3">
          {/* THE ANCHOR AGAIN — quince#819. A password manager keys on (origin, username), and this
              is a THIRD password surface on the same origin. Without it, a manager offers to update
              the entry it already holds for the login form using whatever it sees here. Read-only
              and out of the tab order for the same reasons as on the login form (quince#824). */}
          <input
            type="text"
            name="username"
            autoComplete="username"
            readOnly
            tabIndex={-1}
            value="quince-admin"
            className="hidden"
            aria-hidden="true"
          />
          {/* ABSENT WHEN THERE IS NO PASSWORD TO CONFIRM. Rendering it and expecting a blank is
              what quince#855 filed: a required-looking field that must be left empty, with nothing
              on screen saying so. */}
          {hasPassword ? (
            <div className="flex flex-col gap-1">
              <Label htmlFor="current-password">Current password</Label>
              <Input
                id="current-password"
                type="password"
                autoComplete="current-password"
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
              />
            </div>
          ) : null}
          <div className="flex flex-col gap-1">
            <Label htmlFor="new-password">New password</Label>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </div>
          {changeMsg ? (
            <div className={changeMsg.ok ? "text-sm text-fg" : "text-sm text-danger"}>
              {changeMsg.text}
            </div>
          ) : null}
          <Button type="submit" disabled={changeBusy || next.length === 0}>
            {changeBusy ? "…" : hasPassword ? "Change password" : "Set password"}
          </Button>
        </form>
      </section>

      {/* NOTHING TO REMOVE WHEN THERE IS NO PASSWORD — quince#855. The section below offers a
          destructive action against a thing that does not exist, and its cost list describes a
          state the user is ALREADY IN. Replaced by a statement of that state rather than hidden
          silently: "this option is missing" is a worse answer than "you are already here", and the
          form above is the way out of it. */}
      {!hasPassword ? (
        <section>
          <h2 className="text-sm font-semibold">You sign in with a passkey only</h2>
          <p className="mt-1 max-w-xl text-sm text-muted">
            There is no password on this quince. If you lose the device holding your passkey, the
            only way back is <code className="font-mono text-fg">quince auth reset</code> on the
            machine running quince — which needs console or SSH access to it, and clears every
            passkey and session as well.
          </p>
        </section>
      ) : (
      <section>
        <h2 className="text-sm font-semibold">Sign in with a passkey only</h2>
        <p className="mt-1 max-w-xl text-sm text-muted">
          Remove the password entirely and use Face ID or Touch ID to sign in.
        </p>

        {/* THE COST IS SHOWN BEFORE THE CONFIRMATION, NOT INSIDE IT — D7. A consequence a user reads
            only after committing to a destructive action is a consequence they read too late, and
            this one decides whether the option suits their deployment at all. */}
        <PasswordlessCost />

        {removeErr ? <div className="mt-3 max-w-xl text-sm text-danger">{removeErr}</div> : null}

        {/* NOT DISABLED WHEN IT CANNOT WORK, DELIBERATELY. The server refuses with 409 unless a
            passkey exists for THIS address, and its refusal NAMES the addresses the credentials it
            found belong to. Re-deriving that rule here would be a second implementation of an rpId
            check — the shape `RequireStorage` is commented against, where the server decides and the
            client only decides where to point someone — and a disabled button explains nothing. */}
        {confirming ? (
          <div className="mt-4 flex flex-wrap items-center gap-2">
            <Button type="button" variant="destructive" disabled={removeBusy} onClick={submitRemove}>
              {removeBusy ? "…" : "Yes, remove my password"}
            </Button>
            <Button type="button" variant="outline" disabled={removeBusy} onClick={() => setConfirming(false)}>
              Cancel
            </Button>
          </div>
        ) : (
          <Button type="button" variant="outline" className="mt-4" onClick={() => setConfirming(true)}>
            Remove password
          </Button>
        )}
      </section>
      )}
    </div>
  );
}

// The server's own sentence wherever there is one. `last_credential` and the demo's `unavailable`
// both carry messages that name something this client does not know — which addresses hold
// credentials, and why the demo refuses — so replacing them with generic copy would throw away the
// only useful part of the response.
function messageFor(err: unknown, fallback: string): string {
  if (err instanceof APIError && err.message) return err.message;
  return fallback;
}
