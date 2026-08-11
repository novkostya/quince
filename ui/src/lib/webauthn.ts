import { api, APIError } from "@/lib/api";
import { forgetPasskey, rememberPasskey } from "@/lib/passkeyHint";

// The WebAuthn wire helpers and the registration ceremony, in one place — qn.6k follow-up.
//
// THREE COPIES EXISTED BEFORE THIS FILE: the login hook, the Settings surface and the onboarding
// step each carried their own base64url pair and their own ceremony. That is the shape quince#616
// names as a defect rather than a convention — two identical strings maintained twice, with nothing
// connecting them — and it had already reached three. A fourth surface would have copied it again.

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

type BeginRegistration = {
  ceremony: string;
  options: { publicKey: PublicKeyCredentialCreationOptions };
};

/**
 * registerPasskey runs the full registration ceremony and returns true when a credential was
 * created, false when the user dismissed the authenticator sheet.
 *
 * It THROWS on a real failure — an unsupported tier, an rpId mismatch, a rejected attestation —
 * because both callers show those: the user pressed a button and is owed an answer. Only the
 * dismissal is swallowed, and it is swallowed into `false` rather than into silence, so a caller can
 * tell "nothing happened" from "it worked".
 *
 * MUST BE CALLED FROM A USER GESTURE. `navigator.credentials.create()` requires one, which is why
 * both callers invoke this from a button's own handler rather than from an effect.
 */
// `firstRun` picks the PRE-AUTH pair (qn.6m D5). Two endpoint pairs, one ceremony — the wire shape
// and the browser call are identical, and only the guard on the far side differs, so a second copy
// of this function would be two places to fix the next `bytesToB64url` bug in.
export async function registerPasskey(
  name: string,
  opts: { firstRun?: boolean } = {},
): Promise<boolean> {
  const base = opts.firstRun ? "/api/auth/setup/passkey" : "/api/auth/passkeys/register";
  const begin = await api.post<BeginRegistration>(`${base}/begin`, {});
  const pk = begin.options.publicKey;

  let cred: PublicKeyCredential | null;
  try {
    cred = (await navigator.credentials.create({
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
  } catch (err) {
    // A DISMISSED SHEET IS NOT AN ERROR. `NotAllowedError` also covers a timeout and an
    // already-registered authenticator refusing via excludeCredentials — all three are "the user
    // ends up where they started", and none is worth a red message on the screen.
    if (err instanceof Error && err.name === "NotAllowedError") return false;
    throw err;
  }
  if (!cred) return false;

  const resp = cred.response as AuthenticatorAttestationResponse;
  await api.post(
    `${base}/finish?ceremony=${encodeURIComponent(begin.ceremony)}` +
      `&name=${encodeURIComponent(name)}`,
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
  rememberPasskey();
  return true;
}

type BeginAssertion = {
  ceremony: string;
  options: { publicKey: PublicKeyCredentialRequestOptions };
};

/**
 * signInWithPasskey runs the assertion ceremony and resolves the authenticated status.
 *
 * `conditional` picks the SHAPE of the request, and it is the whole difference between the two ways
 * quince offers a passkey:
 *
 *   - true  — non-modal. The credential appears in the browser's own autofill dropdown and the call
 *             waits indefinitely for the user to pick it. Armed on page load.
 *   - false — modal. The system sheet opens immediately. Only ever from a button, because the user
 *             asked for it: credential presence is undetectable, so an unprompted modal would fire
 *             at people who have no passkey.
 *
 * ONE SHARED CEREMONY FOR BOTH, deliberately. The registration path already cost a bug that only
 * one of three copies would have carried; assertion is not getting three either.
 */
export async function signInWithPasskey(opts: {
  conditional: boolean;
  signal?: AbortSignal;
}): Promise<unknown> {
  const begin = await api.post<BeginAssertion>("/api/auth/passkeys/login/begin", {});
  const pk = begin.options.publicKey;

  const cred = (await navigator.credentials.get({
    ...(opts.conditional ? { mediation: "conditional" as CredentialMediationRequirement } : {}),
    ...(opts.signal ? { signal: opts.signal } : {}),
    publicKey: {
      ...pk,
      challenge: b64urlToBytes(pk.challenge as unknown as string),
      allowCredentials: [],
    },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("no credential");

  const resp = cred.response as AuthenticatorAssertionResponse;
  try {
    const out = await api.post(
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
    // A passkey worked HERE. This is the case registration alone would miss: an iCloud-synced
    // credential created on a phone is usable on a Mac that never registered anything.
    rememberPasskey();
    return out;
  } catch (err) {
    // THE SERVER REJECTED IT — so stop firing the sheet unprompted and let the button be the way
    // back. Deliberately NOT reached for a dismissed sheet: `credentials.get()` throws before this
    // point in that case, so cancelling never disables the feature.
    //
    // quince answers 401 identically for "no such credential" and "assertion rejected", on purpose —
    // telling them apart would answer "does this quince know this passkey" to anyone who asked. The
    // client therefore cannot distinguish them, and does not need to: either way this credential did
    // not work here, which is exactly what the hint tracks.
    if (err instanceof APIError) forgetPasskey();
    throw err;
  }
}
