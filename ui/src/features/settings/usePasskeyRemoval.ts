import * as React from "react";
import { useMutation } from "@tanstack/react-query";

import { APIError } from "@/lib/api";
import { removePasskey } from "@/lib/auth";
import { acceptsOf, onlyPasskey, proveWithPasskey, type Factor, type Present } from "@/lib/reauth";
import { forgetPasskey, passkeyHintCredentialID } from "@/lib/passkeyHint";

// REMOVING A PASSKEY, INCLUDING EVERYTHING THAT CAN GO WRONG WHILE DOING IT.
//
// LIFTED OUT OF `Passkeys.tsx` RATHER THAN COPIED — qn.13 slice 11, D9's *the admin revokes one
// scoped credential from the device page it was issued from*. That gives this rung a SECOND surface
// that removes a credential, and every comment below is a bug this code already paid for once:
// the two-`try` split, the dismissed-sheet rule, the one-factor path, the not-after-an-attempt
// guard. A second copy would be a second place for the next one to be fixed in only half of.
//
// `webauthn.ts`'s own header is the precedent and states the cost in numbers: THREE COPIES of the
// registration ceremony existed before it, and one carried a bug the other two did not.
//
// NO BEHAVIOUR CHANGE IS INTENDED HERE. The state, the mutation and the two error channels are the
// ones `Passkeys.tsx` had, moved; its suite is what pins that, and it passes unchanged.

/**
 * usePasskeyRemoval owns the removal mutation, the reauth challenge it can raise, and the ceremony
 * error the mutation cannot carry.
 *
 * `onRemoved` runs after a successful removal, and invalidating is the CALLER's job. The hook does
 * not import the shared query key: doing so would make this module import `Passkeys.tsx` while
 * `Passkeys.tsx` imports this one, and a cycle between an auth surface and its own machinery is a
 * load-order question nobody should have to reason about. Each caller knows what it is showing.
 *
 * `isLastAt` answers *was that the last credential this browser could use*, which only the caller
 * knows: the Settings list holds every row, and the device page holds one device's. It decides
 * whether the browser's passkey HINT is cleared, and getting it wrong costs a wasted ceremony on
 * the next visit rather than anything a user can be harmed by — so the caller that cannot tell
 * should say `false` and let the credential-id check below carry it.
 */
export function usePasskeyRemoval(opts: {
  onRemoved?: () => void;
  isLastAt?: (id: string) => boolean;
}) {
  // THE ROW BEING CHALLENGED, with what the server said would satisfy it — qn.6o slice 5.
  //
  // SET ONLY FROM THE REFUSAL, never guessed ahead of it. That rule is `qn.6n`'s: the server NAMES
  // the acceptable factors instead of the client inferring them from `last_credential` +
  // `has_password`. Those two are still the DEAD-END signal and are still handled below — they just
  // no longer stand in for a list the server can send.
  const [challenge, setChallenge] = React.useState<{ id: string; accepts: Factor[] } | null>(null);
  // The ceremony's own failure, which the mutation cannot carry: when the assertion is what went
  // wrong, `remove` was never called and has no error to render.
  const [ceremonyErr, setCeremonyErr] = React.useState<string | null>(null);

  const remove = useMutation({
    mutationFn: ({ id, present }: { id: string; present?: Present }) => removePasskey(id, present),
    onSuccess: (_data, { id }) => {
      // REMOVING THE REMEMBERED ONE, OR THE LAST ONE, STOPS THIS BROWSER OFFERING IT. Otherwise the
      // next visit fires a sheet with nothing to offer — the exact wrong-guess the hint exists to
      // prevent, caused by us.
      //
      // TWO CASES SINCE qn.13, because the hint went from a boolean to a credential id (D2.2):
      //
      //   - the LAST credential — nothing can work in this browser any more, which is qn.6k's case
      //     and is unchanged;
      //   - the REMEMBERED credential, even with others left — `allowCredentials` would name a
      //     credential the platform can no longer find, and the sheet reports no passkey available.
      //
      // The second is what the admin does on the device page when they revoke a household member's
      // passkey, so it is the ordinary path rather than an edge — and it is the case the DEVICE
      // page can answer while the "last one" question is one only the Settings list can.
      if (opts.isLastAt?.(id) || passkeyHintCredentialID() === id) {
        forgetPasskey();
      }
      setChallenge(null);
      opts.onRemoved?.();
    },
    onError: (err, { id, present }) => {
      // THE CHALLENGE, DRIVEN BY THE SERVER'S OWN LIST. `reauth_required` here means *present
      // something*, and `accepts` says what would work — with rule 2's exclusions already applied,
      // so the credential being removed is never offered as proof of its own removal.
      //
      // NOT AFTER AN ATTEMPT (`present` set) — that would loop the challenge on a wrong password
      // instead of showing why it was refused.
      //
      // `last_credential` IS A DIFFERENT REFUSAL AND KEEPS ITS OWN BEHAVIOUR: it is the dead end,
      // it carries the server's sentence naming which addresses hold credentials, and it renders as
      // a message rather than as a prompt. D4's rule — a dead end is never an empty challenge — is
      // the server's here, and this is the client half of it.
      if (present || !(err instanceof APIError) || err.code !== "reauth_required") return;
      const accepts = acceptsOf(err);
      if (!accepts) return;
      // ONE FACTOR NEEDS NO CHOOSER — Operator ruling, D5 as amended. With `["passkey"]` the dialog
      // is a chooser with one choice, so the ceremony runs straight from the press the user already
      // made.
      //
      // NO ACTIVATION CONCERN ON THIS PATH, unlike the add row's: a removal ends in a `DELETE`, an
      // ordinary request needing no gesture. `credentials.get()` needs one, and it is two fast local
      // awaits from the click with no sheet in between — comfortably inside what quince#998 measured
      // surviving three awaits AND a completed sheet.
      //
      // A DISMISSED SHEET LEAVES THE ROW ALONE. `mutateAsync` rejects, `remove.isError` renders the
      // reason where it always did, and the Remove button they pressed is the retry — no dialog,
      // then or ever, which is the half of the ruling that took a correction to get right.
      if (onlyPasskey(accepts)) {
        void proveThenRemove(id);
        return;
      }
      setChallenge({ id, accepts });
    },
  });

  // proveThenRemove RUNS THE CEREMONY AND RETRIES, for the one-factor case where no chooser is shown.
  //
  // A FUNCTION RATHER THAN AN INLINE `void (async () => …)()` INSIDE `onError`, because that shape
  // leaked an UNHANDLED REJECTION the moment the sheet was dismissed — caught by `gates-ui` failing
  // while every test passed, which is the only reason it did not ship. A detached promise with no
  // catch is a silent failure by construction.
  async function proveThenRemove(id: string) {
    setCeremonyErr(null);
    // TWO TRY BLOCKS, NOT ONE, AND THE SPLIT IS THE POINT — architect, reviewing quince#1022.
    //
    // One `try` around both calls cannot tell their failures apart. `proveWithPasskey` makes its own
    // `api.post`s to `reauth/begin` and `reauth/finish`, and both throw `APIError` — the same type a
    // refused `DELETE` throws. A guard written for the second silently swallowed the first, so
    // completing a Face ID prompt and having `reauth/finish` answer 401 (expired ceremony, rotated
    // session) left the screen UNCHANGED: the ceremony error was returned past, the mutation never
    // ran so `remove.error` was null, and nothing rendered.
    //
    // A COMPLETED PROMPT THAT CHANGES NOTHING is the *no silent caps or fallbacks* rule broken on an
    // auth surface, and it is exactly what `ceremonyErr` was added for. Catching each failure where
    // its ORIGIN is known is what makes the distinction structural instead of a predicate somebody
    // has to keep correct.
    let proof: string;
    try {
      proof = await proveWithPasskey("remove_passkey", id);
    } catch (err) {
      // A DISMISSED SHEET IS NOT AN ERROR — this rung's rule everywhere. The user declined, nothing
      // happened, and the Remove button they pressed is still the retry.
      if (err instanceof Error && err.name === "NotAllowedError") return;
      setCeremonyErr("Could not confirm with a passkey.");
      return;
    }
    try {
      await remove.mutateAsync({ id, present: { proof } });
    } catch {
      // RENDERED FROM `remove.error`, where it always was. Swallowed here so the refusal is not
      // reported twice — which is what the original guard was for, and all it should ever have
      // covered.
    }
  }

  return { remove, challenge, setChallenge, ceremonyErr };
}
