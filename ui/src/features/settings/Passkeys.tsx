import { SectionHeading } from "@/components/ui/section-heading";
import * as React from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { api, APIError, messageFor } from "@/lib/api";
import { removePasskey } from "@/lib/auth";
import { acceptsOf, onlyPasskey, proveWithPasskey, type Factor, type Present } from "@/lib/reauth";
import { ReauthChallenge } from "@/features/auth/ReauthChallenge";
import { AddPasskeyRow } from "./AddPasskeyRow";
import { forgetPasskey, passkeyHintCredentialID } from "@/lib/passkeyHint";
import { webauthnAvailable } from "@/lib/webauthn";
import { RelativeTime } from "@/components/RelativeTime";
import { Badge } from "@/components/ui/badge";

// The passkey management surface — qn.6k slice 5b, stories 2, 3, 4 and 8.
//
// It has one job the login form does not: TELLING THE TRUTH ABOUT WHERE A CREDENTIAL WORKS. A
// passkey is bound to a domain, the access path is a user choice, and nothing in the protocol warns
// when they stop agreeing — the phone still lists a credential that can no longer sign in anywhere.
// This is the only screen that can say so, and it is why the list endpoint returns the server's own
// rpId rather than letting the browser guess from `location.hostname`.

type Passkey = {
  id: string;
  name: string;
  rp_id: string;
  created_at: string;
  last_used_at: string | null;
  // NULL MEANS ADMIN (qn.13 D9), mirroring the server's own spelling rather than inventing a second
  // one. `?` as well as `| null` because an older payload simply omits the key, and both readings
  // have to land on "admin" — which is the safe direction to be wrong on a screen only the admin
  // can reach.
  scope?: { udid: string } | null;
};

/**
 * scopedTo returns the device a credential is confined to, or "" for an admin credential.
 *
 * ONE READING OF THE FIELD, SHARED. Three surfaces on this page ask *is this row scoped* — the
 * badge, the removal copy and the ordering — and `worksHere` above is this file's own record of what
 * happens when one question gets three implementations: two of them disagreed and it was a blocking
 * review finding. This is that lesson applied before it costs anything.
 *
 * A MISSING `udid` IS NOT A SCOPE. A `scope` object with an empty udid names no device, so it cannot
 * be rendered as *this device only* and cannot be linked anywhere; treating it as admin would be
 * worse, so it is treated as unclassifiable and reported as such by the caller rather than guessed
 * at here.
 */
export function scopedTo(p: { scope?: { udid: string } | null }): string {
  return typeof p.scope?.udid === "string" ? p.scope.udid : "";
}

/** isScoped reports whether a credential is confined to a device rather than administering quince. */
export function isScoped(p: { scope?: { udid: string } | null }): boolean {
  return p.scope !== null && p.scope !== undefined;
}

export type PasskeyList = {
  passkeys: Passkey[];
  rp_id: string;
  supported: boolean;
  // quince#855 — whether an admin PASSWORD exists. On this payload rather than on the pre-auth
  // `auth/status`, so it is disclosed only to a session that is already the admin.
  has_password: boolean;
};

// EXPORTED since qn.6m slice 6b: `PasswordControls` sits on the same page and going passwordless
// changes what this list should say about itself, so it must be able to invalidate it. Shared from
// here rather than redeclared there — two spellings of one query key is a cache that silently
// splits in two.
export const passkeysKey = ["auth", "passkeys"] as const;
const key = passkeysKey;

// ONE QUERY, TWO CONSUMERS. `PasswordControls` needs `has_password` from this same payload, and a
// second `useQuery` on the same key would be a second definition of the fetch to keep in step. React
// Query dedupes by key, so both components share one request and one cache entry.
export function usePasskeyList() {
  return useQuery({ queryKey: key, queryFn: () => api.get<PasskeyList>("/api/auth/passkeys") });
}

// ONE ANSWER TO "DOES A PASSKEY WORK HERE", SHARED BY BOTH SURFACES ON `/settings/auth` — quince#888
// item 2 review. This page asks that question in three places: the per-row warning below, and twice
// inside `PasswordControls`. They were three separate comparisons, and the review's blocking finding
// was exactly two of them disagreeing — one section telling the user to add a passkey for this
// address while the other said the address cannot hold one.
//
// SHARED BECAUSE THEY MUST NOT BE ABLE TO DISAGREE, not to save the lines. Both surfaces already
// import `PasskeyList` from here, so this is the type's own home.
//
// THIS IS DESCRIBING STATE, NOT GATING AN ACTION, and the distinction is why it does not contradict
// the comment on the removal button — that one argues against re-deriving an rpId rule to decide
// whether an action is ALLOWED, which is the server's call. Saying what the user is looking at is
// the client's.
export function rpIDOf(data: PasskeyList | undefined): string {
  return typeof data?.rp_id === "string" ? data.rp_id : "";
}

// A CREDENTIAL BOUND ELSEWHERE CANNOT SIGN IN HERE — qn.6k D2, and nothing in WebAuthn warns about
// it. An UNKNOWN rpId answers false for every credential, which callers must read as "cannot judge"
// rather than "locked out": `credentialState` treats that case as plain passwordless rather than
// accusing, because a wrong lockout warning sends someone to a console for nothing.
export function worksHere(rpID: string, credentialRPID: string): boolean {
  return rpID !== "" && credentialRPID === rpID;
}

// CAN THIS ADDRESS HOLD A PASSKEY AT ALL — a separate question from whether an existing one works
// here, and the one the review's blocking finding turned on. An rpId must be a domain, so at a bare
// IP the answer is no and no certificate changes it. Absent is treated as NOT supported, so nothing
// offers a ceremony this address may be unable to complete.
export function passkeysSupported(data: PasskeyList | undefined): boolean {
  return data?.supported === true;
}



export function Passkeys() {
  const qc = useQueryClient();
  const list = usePasskeyList();

  const invalidate = () => void qc.invalidateQueries({ queryKey: key });

  // THE ROW BEING CHALLENGED, with what the server said would satisfy it — qn.6o slice 5.
  //
  // SET ONLY FROM THE REFUSAL, never guessed ahead of it. That rule is `qn.6n`'s and is unchanged;
  // what changed is that the server now NAMES the acceptable factors instead of the client inferring
  // them from `last_credential` + `has_password`. Those two are still the DEAD-END signal and are
  // still handled below — they just no longer stand in for a list the server can send.
  const [challenge, setChallenge] = React.useState<{ id: string; accepts: Factor[] } | null>(null);
  // The ceremony's own failure, which the mutation cannot carry: when the assertion is what went
  // wrong, `remove` was never called and has no error to render.
  const [ceremonyErr, setCeremonyErr] = React.useState<string | null>(null);

  // proveThenRemove RUNS THE CEREMONY AND RETRIES, for the one-factor case where no chooser is shown.
  //
  // A FUNCTION RATHER THAN AN INLINE `void (async () => …)()` INSIDE `onError`, because that shape
  // leaked an UNHANDLED REJECTION the moment the sheet was dismissed — caught by `gates-ui` failing
  // while every test passed, which is the only reason it did not ship. A detached promise with no
  // catch is a silent failure by construction.
  //
  // A DISMISSED SHEET STAYS QUIET, which is this file's existing rule and `AddPasskeyRow`'s: the user
  // declined, nothing happened, and the Remove button they pressed is still there. Anything else is
  // a real failure and says so, because the mutation never ran and has no error of its own to show.
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
      // A DISMISSED SHEET IS NOT AN ERROR — this file's rule and `AddPasskeyRow`'s. The user
      // declined, nothing happened, and the Remove button they pressed is still the retry.
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
      // passkey, so it is the ordinary path rather than an edge. `webauthn.ts` falls back to the
      // discoverable flow if it happens anyway on some OTHER browser — this only spares THIS one
      // the wasted ceremony, which is why both exist and neither is redundant.
      if ((rows.length <= 1 && rows.some((p) => p.id === id)) || passkeyHintCredentialID() === id) {
        forgetPasskey();
      }
      setChallenge(null);
      invalidate();
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
      // the message below rather than as a prompt. D4's rule — a dead end is never an empty
      // challenge — is the server's here, and this is the client half of it.
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


  // DEFENSIVE ABOUT THE SHAPE, AND NOT AS A STYLE CHOICE. This card is mounted beside the config
  // editor, so a render that throws here takes the WHOLE Settings page with it — including the
  // editor, on a box whose passkey list happens to be malformed or served by an older daemon that
  // has no such endpoint. A missing array must degrade to "no passkeys yet", never to a blank page.
  // Found by an existing test whose shared `api.get` mock returns the config body for every call,
  // which is exactly the malformed-response case.
  const data = list.data;
  const rows = Array.isArray(data?.passkeys) ? data.passkeys : [];
  const rpID = rpIDOf(data);
  // `supported` absent is treated as NOT supported, so the add button stays disabled rather than
  // offering a ceremony this address may not be able to complete.
  const supported = passkeysSupported(data);
  // THE SECOND QUESTION, AND IT IS NOT THE SAME ONE (quince#1076). `supported` is the SERVER's answer
  // about this ADDRESS — false at a bare IP, where an rpId cannot be a domain. `available` is the
  // BROWSER's answer about this CONNECTION: WebAuthn is secure-context-only, so over plain http
  // there is no `PublicKeyCredential` and every ceremony fails before it starts. A domain reached
  // over http answers yes to the first and no to the second, which is the gap this card, the add
  // row, the login button and the passwordless control all fell into.
  //
  // NOT MEMOISED AND NOT IN STATE: it is a property of the document this bundle is running in, so it
  // cannot change without a reload, and holding it in state would add a source of truth that can go
  // stale for nothing.
  const available = webauthnAvailable();
  // `has_password` IS NO LONGER READ HERE, AND THAT IS THE SLICE — qn.6o slice 5. It decided whether
  // to offer the removal fallback: *"absent is treated as NO PASSWORD … it withholds the fallback
  // form rather than offering one against an install that may have nothing to type."* Careful, and
  // still an inference drawn on the client about which factor applies. `accepts` is that answer
  // computed by the side that enforces the rule, so the flag has nothing left to decide.
  //
  // IT REMAINS ON THE PAYLOAD and `PasswordControls` still reads it from the same query — this is
  // one consumer going away, not the field.

  return (
    <div className="mt-8">
      <SectionHeading>Passkeys</SectionHeading>
      <p className="mt-3 text-sm text-muted">
        Sign in with Face ID or Touch ID instead of typing your password. Your password keeps working
        — a passkey is an addition, never a replacement.
      </p>

      {/* STORY 4: REFUSE THE TIER RATHER THAN OFFER A BUTTON THAT CANNOT WORK. An rpId must be a
          domain, so a bare IP cannot host passkeys and no certificate fixes it. Saying so here is
          the difference between "quince does not support this" and "quince is broken". */}
      {data && !supported ? (
        <p className="mt-3 rounded-card border border-line bg-bg px-3 py-2 text-sm text-warn">
          Passkeys need a domain name over https, and you have reached quince at{" "}
          <span className="font-mono">{rpID}</span>. A reverse proxy or Tailscale gives you
          one; an address like this cannot hold a passkey.
        </p>
      ) : null}

      {/* THE OTHER REASON, SAID ONLY WHEN IT IS THE ONE THAT APPLIES (quince#1076). The banner above
          already names https, so on a bare IP over plain http — both true at once — this would be
          the second sentence saying it. It renders where the address is fine and the CONNECTION is
          not, which is a domain reached over http: the case the banner above cannot describe and the
          one where the remedy is different, because https is reachable here and a new hostname is
          not needed. */}
      {data && supported && !available ? (
        <p className="mt-3 rounded-card border border-line bg-bg px-3 py-2 text-sm text-warn">
          Passkeys need an https connection, and you have reached quince over plain http. The
          controls are hidden rather than shown failing — set up https and they come back.
        </p>
      ) : null}

      {list.isLoading ? <p className="mt-3 text-sm text-muted">Loading…</p> : null}
      {list.isError ? <p className="mt-3 text-sm text-danger">Could not load passkeys.</p> : null}


      {data && rows.length === 0 ? (
        <p className="mt-3 text-sm text-muted">No passkeys yet.</p>
      ) : null}

      {data && rows.length > 0 ? (
        <ul className="mt-3 flex flex-col gap-2">
          {rows.map((p) => (
            <li
              key={p.id}
              className="flex flex-wrap items-center justify-between gap-2 rounded-card border border-line bg-bg px-3 py-2"
            >
              <div className="min-w-0">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <div className="truncate text-sm font-medium">{p.name}</div>
                  {/* MARKED, NOT INFERRED — D9. The admin has to be able to answer *what have I
                      issued*, and the row's NAME cannot answer it: a scoped credential's label is
                      derived from its device, but an admin credential may be called anything,
                      including a device's name. Two rows reading `Kitchen iPad` are an admin
                      passkey and a household member's until something says which.

                      BOTH SIDES ARE MARKED, rather than badging only the scoped ones. An unmarked
                      row would mean *admin* by absence, which is the same reasoning `store.Scope`
                      rejects for the column itself: a state you infer from a missing thing is
                      indistinguishable from a state nobody set. */}
                  {isScoped(p) ? (
                    <Badge tone="accent">one device only</Badge>
                  ) : (
                    <Badge tone="neutral">administers quince</Badge>
                  )}
                </div>
                <div className="text-xs text-muted">
                  added <RelativeTime iso={p.created_at} />
                  {p.last_used_at ? (
                    <>
                      {" · last used "}
                      <RelativeTime iso={p.last_used_at} />
                    </>
                  ) : (
                    // NEVER USED IS A FACT WORTH SHOWING, not an empty cell: a credential nobody
                    // has signed in with is exactly the one worth removing.
                    " · never used"
                  )}
                </div>
                {/* THE HAZARD, ON THE ROW IT APPLIES TO. Moving between access paths breaks every
                    passkey while the phone still lists them, and nothing in the protocol says so.
                    Comparing against the SERVER's rpId rather than location.hostname is what makes
                    this agree with what would actually happen. */}
                {rpID && !worksHere(rpID, p.rp_id) ? (
                  <div className="text-xs text-warn">
                    set up for <span className="font-mono">{p.rp_id}</span> — will not work at{" "}
                    <span className="font-mono">{rpID}</span>
                  </div>
                ) : null}

                {/* WHICH DEVICE, AS A PLACE TO GO RATHER THAN A STRING TO READ — D9's *and for a
                    scoped one, which device*.

                    THE LINK IS THE ANSWER AND THE NAME IS NOT. The row's title is the label derived
                    at enrolment, which is a SNAPSHOT: renaming the device afterwards does not
                    rewrite issued credentials, so the label can go stale while the udid cannot.
                    Following the link always lands on the right device.

                    A SCOPE WITH NO UDID NAMES NOTHING, so it gets the badge and no link rather than
                    a link to `/devices/`, which would be a page about no device. It should be
                    unreachable — the column is written from `store.DeviceScope`, which is only
                    constructed with a udid — and rendering it as unlinkable is what keeps a
                    surprise from becoming a broken route. */}
                {isScoped(p) && scopedTo(p) ? (
                  <div className="text-xs text-muted">
                    <Link
                      to={`/devices/${encodeURIComponent(scopedTo(p))}`}
                      className="underline underline-offset-2"
                    >
                      open the device it was issued for
                    </Link>
                  </div>
                ) : null}
              </div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => remove.mutate({ id: p.id })}
                disabled={remove.isPending}
              >
                Remove
              </Button>
            </li>
          ))}
        </ul>
      ) : null}

      {/* THE REFUSAL HAS TO LAND SOMEWHERE. Until quince#888 this mutation could not fail in a way
          the user was meant to act on, so nothing rendered `remove.error` — a 409 would have been
          swallowed and the row would simply have stayed put, which is the silent-fallback shape the
          hard rules forbid. The server's own sentence, because `last_credential` names which
          addresses hold credentials and what to do first; this client knows neither. */}
      {remove.isError ? (
        <p className="mt-3 text-sm text-danger">
          {messageFor(remove.error, "Could not remove the passkey.")}
        </p>
      ) : null}

      {/* THE CEREMONY'S OWN FAILURE, which the mutation above cannot carry: when the assertion is
          what went wrong, `remove` was never called. Never shown for a DISMISSED sheet — the user
          declined and nothing happened, which is not an error on any surface in this rung. */}
      {ceremonyErr ? <p className="mt-3 text-sm text-danger">{ceremonyErr}</p> : null}

      {/* THE CHALLENGE, REPLACING THE BESPOKE PASSWORD FALLBACK — qn.6o slice 5. What stood here was
          a hand-rolled form: a label, a password input, Confirm and Cancel, offered when the client
          inferred from `last_credential` + `has_password` that the password was the way.

          IT WAS CORRECT AND IT WAS THE THIRD SPELLING of one question. `qn.6o`'s whole point is that
          no surface invents its own affordance for asking, and this one predates the shared surface
          rather than disagreeing with it. Deleting it is the consolidation the rung ends on.

          IT ALSO STOPS INFERRING. The old form appeared on a rule this client evaluated —
          `last_credential` plus a `has_password` flag — where `accepts` is computed by the side that
          enforces rule 2, for this target at this address. The dead end keeps its own shape: that is
          `last_credential`, and it renders as the message above rather than as a prompt. */}
      {challenge ? (
        <ReauthChallenge
          operation="remove_passkey"
          target={challenge.id}
          accepts={challenge.accepts}
          title="Confirm it is you"
          subtitle="Removing a passkey changes how you sign in, so quince needs a different credential you have right now."
          // THE DIALOG'S OWN DISMISSAL REPLACED A HAND-ROLLED Cancel BUTTON that sat beside the
          // inline challenge. Escape and the backdrop reach this too, which the button never did.
          onCancel={() => {
            setChallenge(null);
            remove.reset();
          }}
          onProved={async (present) => {
              // STRAIGHT THROUGH, UNLIKE THE ADD PATH. A removal ends in a `DELETE` — an ordinary
              // request needing no user activation — so there is no gesture to preserve and no
              // fresh click to wait for. `AddPasskeyRow` parks here instead, because its ceremony
              // ends in `credentials.create()`, which does need one (D1, quince#988).
            await remove.mutateAsync({ id: challenge.id, present });
          }}
        />
      ) : null}

      {/* D6: THE ADD ROW SITS BELOW THE LIST — the list is what the user came to read, the action is
          what they do after, and a new passkey then appears directly above the row that created it.
          It replaces an *Add a passkey* button that opened `AddPasskeyDialog`; that dialog is gone,
          and with it the fourth copy of the registration ceremony. */}
      {/* `supported && available` — BOTH QUESTIONS, ONE PROP. The row's own contract is "can a
          credential be created here", and until quince#1076 it was answered by the server's half
          alone, so over plain http it rendered live and threw on submit. */}
      <AddPasskeyRow supported={supported && available} onAdded={invalidate} />

      {/* THE rpId HAZARD, STATED WHERE THE CREDENTIAL IS CREATED — the ruling asks for exactly this,
          and not only in the docs. Nothing in WebAuthn warns about it, and the failure looks like
          "no credential here" rather than like a move. */}
      {supported ? (
        <p className="mt-3 text-xs text-muted">
          A passkey is tied to the address you set it up on —{" "}
          <span className="font-mono">{rpID}</span>. If you later reach quince by a different
          name, your passkeys stop working there and you sign in with your password instead.
        </p>
      ) : null}
    </div>
  );
}
