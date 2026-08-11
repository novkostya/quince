import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { api, APIError } from "@/lib/api";
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

type PasskeyList = { passkeys: Passkey[]; rp_id: string; supported: boolean };

const key = ["auth", "passkeys"] as const;

function b64urlToBytes(s: string): Uint8Array {
  const pad = s.length % 4 === 0 ? "" : "=".repeat(4 - (s.length % 4));
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/") + pad);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function bytesToB64url(b: ArrayBuffer): string {
  let bin = "";
  for (const byte of new Uint8Array(b)) bin += String.fromCharCode(byte);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function Passkeys() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: key, queryFn: () => api.get<PasskeyList>("/api/auth/passkeys") });
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  const invalidate = () => void qc.invalidateQueries({ queryKey: key });

  const remove = useMutation({
    mutationFn: (id: string) => api.del<void>(`/api/auth/passkeys/${encodeURIComponent(id)}`),
    onSuccess: invalidate,
  });

  async function addPasskey() {
    setError(null);
    setBusy(true);
    try {
      // NAMED BEFORE THE SHEET APPEARS, not after. The browser's Face ID prompt is modal, and asking
      // for a label on the far side of it means typing while the user has already moved on — and if
      // they dismiss the naming step the credential exists on their device with no row here.
      const name = window.prompt("Name this passkey — the device you are setting it up on:");
      if (!name?.trim()) return;

      const begin = await api.post<{ ceremony: string; options: { publicKey: PublicKeyCredentialCreationOptions } }>(
        "/api/auth/passkeys/register/begin",
        {},
      );
      const pk = begin.options.publicKey;
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
      if (!cred) return;

      const resp = cred.response as AuthenticatorAttestationResponse;
      await api.post(
        `/api/auth/passkeys/register/finish?ceremony=${encodeURIComponent(begin.ceremony)}` +
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
      invalidate();
    } catch (err) {
      // UNLIKE THE LOGIN HOOK, FAILURES HERE ARE SHOWN. The user pressed a button and is owed an
      // answer — the silent-by-design reasoning applies only to the login form, where nobody asked
      // for anything. The server's own message is used verbatim where there is one, because the two
      // that matter (unsupported tier, rpId mismatch) each name a domain, and that is the whole of
      // their value.
      if (err instanceof APIError) setError(err.message);
      else if (err instanceof Error && err.name === "NotAllowedError") setError(null); // dismissed
      else setError(err instanceof Error ? err.message : "Could not add a passkey.");
    } finally {
      setBusy(false);
    }
  }

  // DEFENSIVE ABOUT THE SHAPE, AND NOT AS A STYLE CHOICE. This card is mounted beside the config
  // editor, so a render that throws here takes the WHOLE Settings page with it — including the
  // editor, on a box whose passkey list happens to be malformed or served by an older daemon that
  // has no such endpoint. A missing array must degrade to "no passkeys yet", never to a blank page.
  // Found by an existing test whose shared `api.get` mock returns the config body for every call,
  // which is exactly the malformed-response case.
  const data = list.data;
  const rows = Array.isArray(data?.passkeys) ? data.passkeys : [];
  const rpID = typeof data?.rp_id === "string" ? data.rp_id : "";
  // `supported` absent is treated as NOT supported, so the add button stays disabled rather than
  // offering a ceremony this address may not be able to complete.
  const supported = data?.supported === true;

  return (
    <Card className="mt-6">
      <h2 className="text-base font-semibold">Passkeys</h2>
      <p className="mt-1 text-sm text-muted">
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
      {error ? <p className="mt-3 text-sm text-danger">{error}</p> : null}

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
                {rpID && p.rp_id !== rpID ? (
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
                onClick={() => remove.mutate(p.id)}
                disabled={remove.isPending}
              >
                Remove
              </Button>
            </li>
          ))}
        </ul>
      ) : null}

      <div className="mt-4">
        <Button type="button" onClick={addPasskey} disabled={busy || !supported}>
          {busy ? "Working…" : "Add a passkey"}
        </Button>
      </div>

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
    </Card>
  );
}
