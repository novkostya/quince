// The WebAuthn wire primitives — the base64url pair and the two `begin` response shapes.
//
// SPLIT OUT IN qn.6n SLICE 5b TO BREAK A CYCLE, and the cycle was a signal rather than a nuisance.
// `webauthn.ts` holds the login and registration ceremonies; `reauth.ts` holds the one that proves a
// present credential. Registration now needs to re-authenticate, and re-authentication needs the
// codec — so each file wanted the other.
//
// The server made the same split for the same reason one layer up (spec D3): the reauth pair is
// separate from `passkeys/login/*` because their guards differ, and keeping the shared *mechanics*
// in a third place is what lets both be separate without duplicating the parts that are identical.
//
// NOTHING HERE TOUCHES THE NETWORK OR THE AUTHENTICATOR. It is encoding and types, which is why it
// can be imported by anything without pulling a ceremony along with it.

/** WebAuthn's JSON shape is base64url; its JS API is ArrayBuffer. */
export function b64urlToBytes(s: string): Uint8Array {
  const pad = s.length % 4 === 0 ? "" : "=".repeat(4 - (s.length % 4));
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/") + pad);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function bytesToB64url(b: ArrayBuffer): string {
  let bin = "";
  for (const byte of new Uint8Array(b)) bin += String.fromCharCode(byte);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export type BeginRegistration = {
  ceremony: string;
  options: { publicKey: PublicKeyCredentialCreationOptions };
};

export type BeginAssertion = {
  ceremony: string;
  options: { publicKey: PublicKeyCredentialRequestOptions };
};
