import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { messageFor } from "@/lib/api";
import { changePassword, removePassword } from "@/lib/auth";
import { passkeysKey, usePasskeyList, rpIDOf, worksHere, type PasskeyList } from "@/features/settings/Passkeys";

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
//
// THE ADDRESS IS A SECOND WAY TO LOSE THE CREDENTIAL AND THIS LIST NAMED ONLY THE DEVICE — quince#902.
// A passkey is bound to an `rp_id`, the domain quince derives from the request's `Host`, so a passkey
// that works at one address does not work at another (qn.6k D2, already surfaced per row in the
// `Passkeys` list). On a passwordless install **the address changing is the same event as the device
// being lost**: rename the box, put it behind a proxy, reconfigure one to stop preserving `Host`,
// move to a Tailscale name, switch to an IP where an rpId is impossible at all.
//
// IT IS MORE LIKELY THAN LOSING THE PHONE, AND THIS PAGE RECOMMENDS ONE OF THE TRIGGERS — the
// `Passkeys` card says *"A reverse proxy or Tailscale gives you one"* a few lines up.
//
// AND THE ASYMMETRY IS THE PART A USER CANNOT GUESS: a password is NOT rpId-bound, so an install that
// has one survives every item on this list — sign in with it, register a fresh passkey at the new
// address, no console. Passwordless is the only configuration where a hostname change is
// unrecoverable, which is precisely the decision this panel exists to inform.
//
// SECOND IN THE LIST, NOT FOURTH. The two ways to LOSE the credential belong together; the remaining
// bullets are about the remedy, and a way-to-lose-it arriving after *"there is no way back in at
// all"* reads as an afterthought to a conclusion.
function PasswordlessCost() {
  return (
    <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-muted">
      <li>
        Your passkey becomes the only way in. If you lose the device holding it, the only way back is{" "}
        <code className="font-mono text-fg">quince auth reset</code> on the machine running quince.
      </li>
      <li>
        <span className="font-medium">The address matters as much as the device.</span> A passkey only
        works at the name you set it up on, so renaming this machine, putting it behind a proxy, or
        moving to a Tailscale name breaks it just as completely — and a password would have survived
        all of those.
      </li>
      <li>
        {/* `quince auth reset` NAMED AGAIN RATHER THAN "that command". It is now two bullets from its
            introduction, and a pronoun reaching that far is one insertion away from pointing at the
            wrong thing. */}
        <code className="font-mono text-fg">quince auth reset</code> clears the password,{" "}
        <span className="font-medium">every passkey</span> and every signed-in session — it is a fresh
        start, not a repair.
      </li>
      <li>
        It runs on the machine itself, so it needs console or SSH access.{" "}
        <span className="font-medium">
          If you cannot get a shell on it, there is no way back in at all.
        </span>
      </li>
    </ul>
  );
}

// `has_password: false` MEANS THREE DIFFERENT THINGS AND THIS SURFACE RENDERED ONE — quince#888
// item 2. It said *"This quince has no password — you sign in with a passkey"* for all of them,
// which is a confident description of a configuration the user may not have:
//
//	passwordless        a passkey works at THIS address. The sentence is true.
//	elsewhere-only      passkeys exist, none bound here. NOTHING can sign in at this address.
//	unconfigured        no credentials at all.
//
// ELSEWHERE-ONLY IS THE REACHABLE ONE, and it survives quince#888 item 1. That guard refuses to
// remove the last credential that works HERE, so it cannot be emptied — but an install can arrive in
// this state by being reached at a second address, which is qn.6k D2's whole hazard. The user is then
// told they sign in with a passkey while standing at an address where no passkey of theirs works.
//
// UNCONFIGURED is now hard to reach through the UI and is still rendered honestly, because *"assume
// the safe one"* is what produced this bug: the surface picked the reassuring reading of an ambiguous
// field rather than the one it could actually establish. `quince auth reset` clears sessions too, so
// nobody sees this screen after one — but a hand-edited DB, or a future path nobody has thought of,
// should not be met with an inviting sentence about a passkey that does not exist.
//
// AN UNKNOWN rpId IS NOT AN ACCUSATION. If the payload carries no `rp_id`, this cannot judge which
// credentials work here, so it reports plain `passwordless` rather than claiming the user is locked
// out. A wrong lockout warning would send someone to the console for nothing.
type CredentialState = "has-password" | "passwordless" | "elsewhere-only" | "unconfigured";

function credentialState(data: PasskeyList | undefined, hasPassword: boolean): CredentialState {
  if (hasPassword) return "has-password";
  const rows = Array.isArray(data?.passkeys) ? data.passkeys : [];
  if (rows.length === 0) return "unconfigured";
  const rpID = rpIDOf(data);
  if (!rpID) return "passwordless";
  return rows.some((p) => worksHere(rpID, p.rp_id)) ? "passwordless" : "elsewhere-only";
}

// The rpIds the credentials DO belong to, so the warning can name them. Same reasoning as the
// server's `last_credential` message: "your passkeys do not work here" at a box that visibly lists
// some reads as quince being broken, where naming the address it wants is an instruction.
function boundElsewhere(data: PasskeyList | undefined): string[] {
  const rows = Array.isArray(data?.passkeys) ? data.passkeys : [];
  return [...new Set(rows.map((p) => p.rp_id).filter(Boolean))];
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
  const credentials = credentialState(list.data, hasPassword);
  const elsewhere = boundElsewhere(list.data);
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
      // THE SAME ASSUMPTION AS THE HEADINGS, IN THE PLACE IT IS HARDEST TO SPOT — quince#888 item 2.
      // *"as well as your passkey"* holds only in the `passwordless` state; in the other two the
      // password just set is the ONLY thing that can sign in here, which is both the more important
      // fact and the opposite reassurance.
      setChangeMsg({
        ok: true,
        text:
          credentials === "has-password"
            ? "Password changed. Your other devices stay signed in."
            : credentials === "passwordless"
              ? "Password set. You can now sign in with it as well as your passkey."
              : "Password set. This is now the only way to sign in at this address.",
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
        {/* ONE SENTENCE PER STATE, and the two that are not `passwordless` are WARNINGS rather than
            explanations: in both, setting a password is not an improvement to a working setup, it is
            the repair for one that cannot sign anybody in at this address. */}
        {credentials === "passwordless" ? (
          <p className="mt-1 max-w-xl text-sm text-muted">
            This quince has no password — you sign in with a passkey. Adding one gives you a second
            way in that does not depend on a device.
          </p>
        ) : null}
        {/* NAMED ADDRESSES ONLY WHEN THERE ARE ANY. A row with an empty `rp_id` cannot be produced by
            this server, but it would have rendered "registered for ." rather than degrading.

            THE `passkeysSupported` CONDITION THAT USED TO GUARD THE SECOND REMEDY IS GONE WITH THE
            REMEDY. quince#888 item 2's review made *"or add a passkey for this address"* conditional,
            because at a bare IP no credential can ever match and the sentence pointed at something
            the user could not do. Rule 1 now refuses that remedy in this state for EVERY address, so
            the condition guards nothing — and a condition on a clause that no longer exists is the
            archaeology the Operator ruled out on quince#595. The principle it served survives where
            it belongs: `Passkeys` still disables its own Add button on an unsupported tier. */}
        {/* BOTH REMEDIES THIS PARAGRAPH USED TO OFFER ARE NOW REFUSED — qn.6n D8, slice 7. It ended
            *"Set a password to fix that, or add a passkey for this address"*, and rule 1 closed both:
            each is a change to the credential set, so each demands a PRESENT credential, and here the
            user has none that works. The copy was TRUE on `main` until the rung landed, which is why
            it changes in this diff and not earlier — fixing it sooner would have sent somebody to a
            console to escape a state the form really did fix. */}
        {credentials === "elsewhere-only" ? (
          <p className="mt-1 max-w-xl text-sm text-warn">
            This quince has no password, and none of its passkeys works at this address
            {elsewhere.length > 0 ? (
              <>
                {" — they are registered for "}
                <span className="font-mono">{elsewhere.join(", ")}</span>
              </>
            ) : null}
            . A passkey only works at the address it was created on, so nothing can sign in here at
            the moment — and the form below cannot fix it, because changing what can sign in now
            requires proving something that already can.
          </p>
        ) : null}
        {credentials === "unconfigured" ? (
          <p className="mt-1 max-w-xl text-sm text-danger">
            This quince has no password and no passkeys — there is nothing to sign in with. Set a
            password now: your session is currently the only access to this install.
          </p>
        ) : null}
        <form onSubmit={submitChange} className="mt-3 max-w-sm space-y-3">
          {/* THE ANCHOR AGAIN — quince#819. A password manager keys on (origin, username), and this
              is a THIRD password surface on the same origin. Without it, a manager offers to update
              the entry it already holds for the login form using whatever it sees here. Read-only
              and out of the tab order for the same reasons as on the login form (quince#824).

              VISIBLE, AND IT WAS `className="hidden" aria-hidden="true"` UNTIL THIS COMMIT —
              Operator-reported. That is `display:none`, which is the one variant quince#819 ruled
              AGAINST: the report came from Safari, no authoritative WebKit source was found either
              way, and *"a `display:none` field is the variant most likely to be ignored. Visible is
              the option that cannot be skipped for being invisible."* `PasswordForm` and
              `EncryptionDialog` both carry the ruled shape; this surface was the odd one out.

              IT IS ALSO THE SURFACE WHERE BEING IGNORED COSTS MOST. On the login form a missed
              anchor means the manager files an origin-only entry. Here it means a CHANGE is filed as
              a NEW entry rather than an update — two credentials for `quince-admin` with nothing to
              say which is current, which is exactly the case quince#819's follow-up names as still
              owed a device.

              NOT `disabled`, and not `hidden`: both are skipped by autofill, which is the whole
              point of the field. */}
          <div className="flex flex-col gap-1">
            <Label htmlFor="change-username">Username</Label>
            <Input
              id="change-username"
              name="username"
              type="text"
              autoComplete="username"
              readOnly
              tabIndex={-1}
              value="quince-admin"
            />
          </div>
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
      {credentials === "passwordless" ? (
        <section>
          <h2 className="text-sm font-semibold">You sign in with a passkey only</h2>
          <p className="mt-1 max-w-xl text-sm text-muted">
            There is no password on this quince. If you lose the device holding your passkey, the
            only way back is <code className="font-mono text-fg">quince auth reset</code> on the
            machine running quince — which needs console or SSH access to it, and clears every
            passkey and session as well.
          </p>
        </section>
      ) : null}
      {/* THE TWO STATES SPLIT HERE — qn.6n D8, slice 7, and quince#903. They shared one sentence
          because rule 1 did not exist: on `main` before this rung, `PUT /api/auth/password` accepted
          an absent `current_password`, so *"use the form above"* was true in BOTH. Rule 1's exemption
          is `Configured()`, which is exactly what separates them:

            unconfigured    no credentials at all → NOT configured → exempt. The form still works.
            elsewhere-only  passkeys exist, none here → CONFIGURED → rule 1 applies. The form does not.

          quince#895 split this section from `passwordless`, correctly, and one split short. */}
      {credentials === "unconfigured" ? (
        <section>
          <h2 className="text-sm font-semibold">This quince has no way to sign in</h2>
          <p className="mt-1 max-w-xl text-sm text-muted">
            Use the form above — you are signed in now, so you can set a password without console
            access. <code className="font-mono text-fg">quince auth reset</code> is not the way back
            from here: it clears credentials rather than restoring them, and you would still have to
            set one afterwards.
          </p>
        </section>
      ) : null}
      {/* AND THE CHEAPER REMEDY IS NAMED FIRST, WHICH D8 DID NOT ANTICIPATE. The spec's analysis
          concluded that `quince auth reset` was *"what is genuinely true there"* — it is true, and it
          is not the only thing. A passkey registered for another address still WORKS at that address:
          reach quince there, and setting a password satisfies rule 1 with the credential you have.
          The password then works everywhere, including here.

          Verified rather than assumed before this copy was written: `provable` imposes no restriction
          on `set_password`, and `FinishReauth` resolves the credential against the ceremony's own
          rpId — so an assertion at the address the passkey belongs to mints a usable proof.

          CONDITIONAL, because reachability is the user's fact and not ours. A name in the credential
          list may be a tunnel they no longer run, so this offers the route and the console both,
          rather than promising one that may not exist. */}
      {credentials === "elsewhere-only" ? (
        <section>
          <h2 className="text-sm font-semibold">No passkey of yours works at this address</h2>
          <p className="mt-1 max-w-xl text-sm text-muted">
            The form above cannot help here: setting a password now requires proving a credential
            that already works, and none of yours does at this address.
            {elsewhere.length > 0 ? (
              <>
                {" If you can still reach quince at "}
                <span className="font-mono">{elsewhere.join(" or ")}</span>
                {", open it there — your passkey works at that address, and a password you set from " +
                  "there will work everywhere, including here."}
              </>
            ) : null}{" "}
            Otherwise the way back is{" "}
            <code className="font-mono text-fg">quince auth reset</code> at the console, which clears
            every credential and every session and returns this install to first-run setup.
          </p>
        </section>
      ) : null}
      {hasPassword ? (
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
      ) : null}
    </div>
  );
}
