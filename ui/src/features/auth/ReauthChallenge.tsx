import { PasswordForm } from "./PasswordForm";
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
}) {
  const wantsPassword = accepts.includes("password");
  const wantsPasskey = accepts.includes("passkey");

  return (
    <PasswordForm
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
  );
}
