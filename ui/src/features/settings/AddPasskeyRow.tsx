import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { APIError } from "@/lib/api";
import { registerPasskey } from "@/lib/webauthn";
import { ReauthChallenge, type Factor } from "@/features/auth/ReauthChallenge";

// ADD A PASSKEY, INLINE — qn.6o slice 4, D6. Operator ruling:
//
//   "I don't want 2 dialogs in a row. Which means either custom dialog for passkey addition case,
//    or move passkey name input to the page itself prior to challenge."
//
// The name goes on the page. It is a PARAMETER OF THE ACTION, not a challenge — like the
// New-password field already sitting inline in `PasswordControls` on this same page, which makes
// the two halves of Settings consistent for the first time.
//
// THIS RETIRES `AddPasskeyDialog`, and that file held its OWN copy of the registration ceremony —
// the fourth copy `lib/webauthn.ts` was created to prevent, and the reason quince#930's fix never
// reached this surface. Deleting it is most of the repair.
//
// NOTHING FORCES THE ORDERING, recorded so a later reader does not think it does: the name is used
// only at `register/finish`, as a query parameter, and plays no part in `begin` or in `create()`.
// Collecting it up front is a UX choice.

// acceptsOf pulls `accepts` off a refusal — qn.6o slice 2's field.
//
// READ OFF `details`, WHICH IS THE WHOLE PARSED BODY, rather than promoted to a field on `APIError`.
// That class carries `code`, `message` and the raw body, and adding a typed field for one code's
// one extra key would put a `reauth_required`-shaped hole in a type every error in the product uses.
//
// NARROWED RATHER THAN CAST. The values come off the wire, so a `Factor[]` assertion here would be
// a promise this code cannot keep — an older or newer daemon sending a factor this build has never
// heard of would flow straight into the challenge and render nothing for it.
function acceptsOf(err: APIError): Factor[] | undefined {
  const body = err.details;
  if (!body || typeof body !== "object" || !("error" in body)) return undefined;
  const list = (body as { error?: { accepts?: unknown } }).error?.accepts;
  if (!Array.isArray(list)) return undefined;
  const known = list.filter((f): f is Factor => f === "password" || f === "passkey");
  return known.length > 0 ? known : undefined;
}

// The three states this row can be in. A union rather than three booleans, because two of them
// being true at once is the bug — a challenge showing while a proof is already in hand.
type Stage =
  | { at: "idle" }
  // The server refused and said what would satisfy it. `accepts` is passed through unread.
  | { at: "challenge"; accepts: Factor[] }
  // A factor satisfied the server. WAITING FOR A FRESH CLICK — see below.
  | { at: "proved"; present: { current_password: string } | { proof: string } };

export function AddPasskeyRow({
  supported,
  onAdded,
}: {
  // False at a bare IP, where no certificate can help — D9. The row still renders, disabled, with
  // the explanation the card already carries above it: `qn.6g`'s rule is that a remedy the user
  // cannot follow is the same defect as a silent failure, and a live-looking field that fails on
  // submit is exactly that.
  supported: boolean;
  onAdded: () => void;
}) {
  const [name, setName] = useState("");
  const [stage, setStage] = useState<Stage>({ at: "idle" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function reset() {
    setStage({ at: "idle" });
    setName("");
    setError(null);
  }

  // create RUNS THE CEREMONY, AND EVERY CALLER OF IT IS A CLICK HANDLER. That is the whole of D1's
  // corrected constraint (quince#988): `navigator.credentials.create()` needs transient activation,
  // and there is at most ONE await — `register/begin` — between the click and the sheet.
  async function create(present: { current_password: string } | { proof: string } | undefined) {
    setBusy(true);
    setError(null);
    try {
      const added = await registerPasskey(name.trim(), {
        ...(present && "current_password" in present
          ? { currentPassword: present.current_password }
          : {}),
        ...(present && "proof" in present ? { proof: present.proof } : {}),
      });
      if (added) {
        onAdded();
        reset();
        return;
      }
      // A DISMISSED SHEET IS NOT AN ERROR and `registerPasskey` returns false for it. Say nothing
      // red; leave the row as it was so the button is there to press again.
      setStage({ at: "idle" });
    } catch (err) {
      if (err instanceof APIError && err.code === "reauth_required") {
        // THE REFUSAL CARRIES WHAT WOULD SATISFY IT (slice 2). Rendering the challenge from
        // `accepts` is the whole design; deriving it here would be the second copy of a rule the
        // server already owns.
        //
        // `accepts` ABSENT IS NOT AN EMPTY CHALLENGE. The server omits the field only where nothing
        // could satisfy the operation, and sends `last_credential` for that case — so an absent
        // list here means an older daemon, and the honest answer is to say so rather than to render
        // a prompt with no controls in it (D4).
        const accepts = acceptsOf(err);
        if (accepts && accepts.length > 0) {
          setStage({ at: "challenge", accepts });
        } else {
          setError("This quince needs a credential to add a passkey, but did not say which.");
        }
      } else if (err instanceof APIError) {
        setError(err.message);
      } else {
        setError("Could not add the passkey.");
      }
    } finally {
      setBusy(false);
    }
  }

  if (stage.at === "challenge") {
    return (
      <ReauthChallenge
        operation="add_passkey"
        accepts={stage.accepts}
        title="Confirm it is you"
        subtitle="Adding a passkey changes how you sign in, so quince needs a credential you have right now."
        onProved={async (present) => {
          // THE PASSWORD PATH GOES STRAIGHT THROUGH, and it is correct to: the user's click on the
          // challenge's own Confirm button is fresh activation, and `register/begin` is the single
          // await before `create()` — the measured shape.
          if ("current_password" in present) {
            await create(present);
            return;
          }
          // THE PASSKEY PATH MUST NOT. The gesture was spent on the proof's own authenticator
          // sheet, and completing a sheet grants no new activation — so chaining `create()` here
          // is quince#976 exactly. Park the proof and ask for one more click (D1, quince#988).
          setStage({ at: "proved", present });
        }}
      />
    );
  }

  return (
    <div className="mt-4">
      {/* D7: THE SAME GEOMETRY AS A LIST ITEM, AND NO `border-dashed`. In this product a dashed
          border means ABSENT or BROKEN — consistently, across five sites — so a dashed add-row here
          would read as *a passkey that is broken*, on the one screen that warns when a credential
          has stopped working at this address. The content carries the difference instead: a text
          input and a button do not look like a row of text. */}
      <div className="flex flex-wrap items-center gap-2 rounded-card border border-line bg-bg px-3 py-2">
        <div className="min-w-0 flex-1">
          {/* D8: THE LABEL IS VISUALLY HIDDEN, NOT ABSENT. The placeholder is a HINT — it
              disappears the moment the user types, and a screen reader announces an unnamed field.
              This codebase already pays that cost deliberately: the `Device` anchor on the login
              form is labelled, `readOnly` and `tabIndex={-1}` with a paragraph explaining why. */}
          <Label htmlFor="passkey-name" className="sr-only">
            Passkey name
          </Label>
          <Input
            id="passkey-name"
            value={name}
            disabled={!supported || busy}
            onChange={(e) => setName(e.target.value)}
            // AN EXAMPLE, NOT AN INSTRUCTION. "Enter a name" would occupy the same space saying
            // less; a real-looking name shows both what the field is and what a good answer is.
            placeholder="my iPhone"
          />
        </div>
        {stage.at === "proved" ? (
          // THE FRESH CLICK — D1 as corrected on quince#988, and the reason this stage exists.
          // Its handler is the last user gesture before `create()`, with one await between.
          <Button type="button" disabled={busy} onClick={() => create(stage.present)}>
            {busy ? "…" : "Create the passkey"}
          </Button>
        ) : (
          <Button
            type="button"
            disabled={!supported || busy || name.trim() === ""}
            onClick={() => create(undefined)}
          >
            {busy ? "…" : "Add"}
          </Button>
        )}
      </div>
      {stage.at === "proved" ? (
        <p className="mt-2 text-sm text-muted">
          Confirmed. Press <span className="font-medium">Create the passkey</span> to make it — your
          device will ask once more.
        </p>
      ) : null}
      {error ? <p className="mt-2 text-sm text-danger">{error}</p> : null}
    </div>
  );
}
