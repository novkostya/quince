import { useEffect, useRef } from "react";

import { api } from "@/lib/api";
import type { AuthStatus } from "@/lib/types";

// CONDITIONAL MEDIATION — qn.6k slice 4, the login surface the Operator ruled for on quince#657.
//
// The passkey is offered inside the browser's own autofill dropdown, beside saved passwords, rather
// than through a modal. That shape is forced by a fact rather than chosen for taste: THERE IS NO WAY
// TO DETECT THAT A PASSKEY IS REGISTERED. No API answers it — it would be a fingerprinting vector —
// and `isConditionalMediationAvailable()` reports BROWSER CAPABILITY, not credential existence. So
// an unconditional `navigator.credentials.get()` on page load would fire a modal at every user who
// has no passkey, and the only way to find out they have none is to prompt and fail.
//
// Everything here is therefore additive and silent: if it cannot run, or finds nothing, or is
// aborted, the password form the user is already looking at is untouched and still works.

// b64urlToBytes / bytesToB64url — WebAuthn's JSON shape is base64url and its JS API is ArrayBuffer.
// Written out rather than pulled in: two small functions against a dependency whose only job is
// this, on the one surface where a supply-chain addition is least welcome.
function b64urlToBytes(s: string): Uint8Array {
  const pad = s.length % 4 === 0 ? "" : "=".repeat(4 - (s.length % 4));
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/") + pad);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function bytesToB64url(b: ArrayBuffer): string {
  const bytes = new Uint8Array(b);
  let bin = "";
  for (const byte of bytes) bin += String.fromCharCode(byte);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

type BeginResponse = { ceremony: string; options: { publicKey: PublicKeyCredentialRequestOptions } };

/**
 * usePasskeyLogin arms conditional mediation on mount and calls onSuccess when a passkey signs in.
 *
 * It resolves nothing and rejects nothing that the caller must handle: a passkey login either
 * happens, or the user types their password. That is the whole contract.
 */
export function usePasskeyLogin(onSuccess: (status: AuthStatus) => void): void {
  // A ref, so a re-render caused by typing in the password field cannot re-arm a second ceremony
  // beside the one already waiting.
  const armed = useRef(false);

  useEffect(() => {
    if (armed.current) return;
    armed.current = true;

    // A CHEAP EXPLICIT CHECK, AND IT IS NOT THE LOAD-BEARING ONE — measured, not assumed. Removing
    // it changes nothing observable: with `PublicKeyCredential` absent, the property access below
    // throws and the same `catch` absorbs it. It stays because "we do not do this here" is worth
    // saying out loud on an optional feature, not because it guards anything.
    //
    // THE GUARD THAT MATTERS IS THE `isConditionalMediationAvailable()` CHECK BELOW: without it,
    // `navigator.credentials.get({mediation: "conditional"})` runs on a browser that cannot do it,
    // which is the user-visible error on a page nobody asked to do anything on yet.
    const supported =
      typeof window.PublicKeyCredential !== "undefined" &&
      typeof window.PublicKeyCredential.isConditionalMediationAvailable === "function";
    if (!supported) return;

    // ABORTED ON UNMOUNT. A conditional ceremony waits indefinitely by design — it is listening to
    // an autofill dropdown — so leaving the page without aborting leaves it pending against a
    // component that is gone.
    const abort = new AbortController();
    let cancelled = false;

    void (async () => {
      try {
        if (!(await window.PublicKeyCredential.isConditionalMediationAvailable())) return;

        const begin = await api.post<BeginResponse>("/api/auth/passkeys/login/begin", {});
        if (cancelled) return;

        const pk = begin.options.publicKey;
        const cred = (await navigator.credentials.get({
          mediation: "conditional",
          signal: abort.signal,
          publicKey: {
            ...pk,
            challenge: b64urlToBytes(pk.challenge as unknown as string),
            allowCredentials: [],
          },
        })) as PublicKeyCredential | null;
        if (!cred || cancelled) return;

        const resp = cred.response as AuthenticatorAssertionResponse;
        const status = await api.post<AuthStatus>(
          `/api/auth/passkeys/login/finish?ceremony=${encodeURIComponent(begin.ceremony)}`,
          {
            id: cred.id,
            rawId: bytesToB64url(cred.rawId),
            type: cred.type,
            response: {
              clientDataJSON: bytesToB64url(resp.clientDataJSON),
              authenticatorData: bytesToB64url(resp.authenticatorData),
              signature: bytesToB64url(resp.signature),
              userHandle: resp.userHandle ? bytesToB64url(resp.userHandle) : null,
            },
          },
        );
        if (!cancelled) onSuccess(status);
      } catch {
        // SILENT BY DESIGN, AND THIS IS THE ONE PLACE THAT DESERVES DEFENDING. Every failure here
        // is either "this user has no passkey" (indistinguishable from the rest, by design), "the
        // browser cannot do this", "the server has no endpoint", or "the user dismissed the sheet".
        // None of them is something the user asked for or can act on — they are looking at a
        // password form that works. Surfacing an error would turn an optional convenience into an
        // apparent fault on the login screen.
        //
        // What is NOT swallowed is a password login failing: that path is untouched and still
        // reports its own errors.
      }
    })();

    return () => {
      cancelled = true;
      abort.abort();
    };
  }, [onSuccess]);
}
