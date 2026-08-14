import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { api, APIError, messageFor } from "@/lib/api";
import { removePasskey } from "@/lib/auth";
import { acceptsOf, type Factor, type Present } from "@/lib/reauth";
import { ReauthChallenge } from "@/features/auth/ReauthChallenge";
import { AddPasskeyRow } from "./AddPasskeyRow";
import { forgetPasskey } from "@/lib/passkeyHint";
import { RelativeTime } from "@/components/RelativeTime";

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
};

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

  const remove = useMutation({
    mutationFn: ({ id, present }: { id: string; present?: Present }) => removePasskey(id, present),
    onSuccess: (_data, { id }) => {
      // REMOVING THE LAST ONE STOPS THE UNPROMPTED SHEET. Otherwise the next visit fires a sheet
      // with nothing to offer — the exact wrong-guess the hint exists to prevent, caused by us.
      //
      // Only when it was the last: with others left, a passkey can still work in this browser, and
      // the removed one may not even have been this device's.
      if (rows.length <= 1 && rows.some((p) => p.id === id)) forgetPasskey();
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
      if (accepts) setChallenge({ id, accepts });
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
      <h2 className="text-sm font-semibold text-muted">Passkeys</h2>
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
                <div className="truncate text-sm font-medium">{p.name}</div>
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
        <div className="mt-3">
          <ReauthChallenge
            operation="remove_passkey"
            target={challenge.id}
            accepts={challenge.accepts}
            title="Confirm it is you"
            subtitle="Removing a passkey changes how you sign in, so quince needs a different credential you have right now."
            onProved={async (present) => {
              // STRAIGHT THROUGH, UNLIKE THE ADD PATH. A removal ends in a `DELETE` — an ordinary
              // request needing no user activation — so there is no gesture to preserve and no
              // fresh click to wait for. `AddPasskeyRow` parks here instead, because its ceremony
              // ends in `credentials.create()`, which does need one (D1, quince#988).
              await remove.mutateAsync({ id: challenge.id, present });
            }}
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="mt-2"
            onClick={() => {
              setChallenge(null);
              remove.reset();
            }}
          >
            Cancel
          </Button>
        </div>
      ) : null}

      {/* D6: THE ADD ROW SITS BELOW THE LIST — the list is what the user came to read, the action is
          what they do after, and a new passkey then appears directly above the row that created it.
          It replaces an *Add a passkey* button that opened `AddPasskeyDialog`; that dialog is gone,
          and with it the fourth copy of the registration ceremony. */}
      <AddPasskeyRow supported={supported} onAdded={invalidate} />

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
