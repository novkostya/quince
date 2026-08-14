import { PasswordForm } from "./PasswordForm";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { proveWithPasskey, type ProofOperation, type Factor } from "@/lib/reauth";

// THE ONE CHALLENGE — qn.6o slice 3, D5. When a credential-changing operation is refused for want
// of a present credential, this is what asks for one, everywhere.
//
// IT RENDERS FROM `accepts` ALONE and derives nothing (D1/G5). The server computed which factors
// would satisfy THIS operation on THIS install; a client that re-derived that rule would be the
// second copy of it, and the copies would drift on exactly the case they are hardest to test —
// rule 2's exclusions.
//
// IT REPLACES AFFORDANCES RATHER THAN ADDING ONE. Without it this rung would put a password field
// on Add-a-passkey beside the bespoke fallback `qn.6n` slice 6b built for removal — a third ad-hoc
// way of asking one question, which is quince#908 §4's *"four different kinds of thing wearing one
// costume"*, arrived at one surface at a time.
//
// TWO CALLERS SINCE SLICE 5 — the add row and the passkey list's removal. It landed with none, on
// the ordering `qn.6n` wrote for itself after shipping a server demand ahead of any client that
// could satisfy it.
//
// IT IS A DIALOG, LIKE EVERY OTHER CONFIRMATION IN THE PRODUCT — Operator-reported 2026-08-14, from
// a screenshot of the shipped build. It rendered INLINE, and worse: reusing `PasswordForm` meant
// reusing `AuthPage`, so a Settings page grew the sign-in screen's WORDMARK and a `min-h-dvh`
// wrapper mid-scroll. Nobody chose that — it arrived with the composition, and jsdom renders no
// layout, so every test passed while the screen was wrong.
//
// IT DOES NOT REOPEN THE OPERATOR'S RULING. *"I don't want 2 dialogs in a row"* is why the passkey
// NAME sits on the page (D6). With the name inline this is the ONLY dialog in the flow — one, not
// two — so being a dialog satisfies that ruling rather than bending it.
//
// AND IT COSTS NO USER ACTIVATION. The gesture that matters is the click on a button INSIDE the
// dialog, which is an ordinary one; opening a dialog neither grants nor spends anything. D1's
// constraint is about awaits between that click and the sheet, and there are none here.

// `Factor` and `Present` MOVED TO `lib/reauth.ts` when the second caller arrived. Re-exported here
// so existing importers keep working and this file still reads as where the challenge's vocabulary
// is defined.
export type { Factor, Present } from "@/lib/reauth";

export function ReauthChallenge({
  operation,
  target,
  accepts,
  title = "Confirm it is you",
  subtitle = "This changes how you sign in, so quince needs a credential you have right now.",
  onProved,
  onCancel,
}: {
  operation: ProofOperation;
  // The credential being removed, for `remove_passkey` only — it travels to `reauth/begin` so the
  // ceremony can exclude it from its own allow-list (rule 2, one layer before the subject check).
  target?: string;
  // WHAT THE SERVER SAID, PASSED THROUGH UNREAD. Never computed here.
  accepts: Factor[];
  title?: string;
  subtitle?: string;
  // Called with the proof once a factor has actually satisfied the server. The caller retries its
  // own operation with it; this component knows nothing about what that operation is beyond the
  // name it was handed.
  onProved: (proof: { current_password: string } | { proof: string }) => Promise<void>;
  // REQUIRED, NOT OPTIONAL. A dialog can always be dismissed — Escape, the backdrop, the close
  // button — so a caller that could omit this would have a state its user can reach and it cannot.
  onCancel: () => void;
}) {
  const wantsPassword = accepts.includes("password");
  const wantsPasskey = accepts.includes("passkey");

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        // CLOSING IS CANCELLING, and it is the ONE way out — Escape, the backdrop and the close
        // button all arrive here, so there is no dismissal path that leaves the caller believing a
        // challenge is still on screen. `open` is a constant because the caller decides existence:
        // it renders this component when it holds a refusal and stops when it does not.
        if (!next) onCancel();
      }}
    >
      <DialogContent>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{subtitle}</DialogDescription>
        <PasswordForm
          // `bare` — the dialog already draws the card, the backdrop and the blur, and `DialogTitle`
          // above is the accessible name. Without it this rendered the sign-in screen's wordmark and
          // a full-viewport wrapper inside a Settings page.
          variant="bare"
          title={title}
          subtitle={subtitle}
          cta="Confirm"
      // D5: `passkeys` STAYS OFF. That prop arms conditional mediation — browser autofill on load —
      // and a challenge must be modal. It is not passed at all rather than passed `false`, so there
      // is no value here for a future edit to flip.
      password={wantsPassword}
      passkeyProof={
        wantsPasskey
          ? {
              cta: "Use a passkey",
              // ONE AWAIT BETWEEN THE CLICK AND THE SHEET. `proveWithPasskey` issues
              // `reauth/begin` and calls `credentials.get()` in the same handler — the measured
              // shape (D1). Do not verify anything before this line: user activation does not
              // survive an extra round trip, and the failure is a `NotAllowedError` that looks
              // exactly like the user dismissing the sheet.
              run: async () => {
                const proof = await proveWithPasskey(operation, target);
                await onProved({ proof });
              },
            }
          : undefined
      }
          onSubmit={async (current_password) => {
            await onProved({ current_password });
          }}
        />
      </DialogContent>
    </Dialog>
  );
}
