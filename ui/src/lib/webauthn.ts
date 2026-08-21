import { api, APIError } from "@/lib/api";
import { forgetPasskey, passkeyHintCredentialID, rememberPasskey } from "@/lib/passkeyHint";
// RE-EXPORTED so the many existing importers of these helpers keep working — the split in
// `webauthnWire.ts` is structural and is not worth a rename sweep across the UI.
export {
  b64urlToBytes,
  bytesToB64url,
  type BeginRegistration,
  type BeginAssertion,
} from "@/lib/webauthnWire";
import { b64urlToBytes, bytesToB64url } from "@/lib/webauthnWire";
import type { BeginRegistration, BeginAssertion } from "@/lib/webauthnWire";

// The WebAuthn wire helpers and the registration ceremony, in one place — qn.6k follow-up.
//
// THREE COPIES EXISTED BEFORE THIS FILE: the login hook, the Settings surface and the onboarding
// step each carried their own base64url pair and their own ceremony. That is the shape quince#616
// names as a defect rather than a convention — two identical strings maintained twice, with nothing
// connecting them — and it had already reached three. A fourth surface would have copied it again.

/**
 * webauthnAvailable reports whether this BROWSER, on this connection, can run a ceremony at all.
 *
 * IT IS A DIFFERENT QUESTION FROM `passkeysSupported`, AND CONFLATING THEM IS quince#1076. That one
 * asks the SERVER whether this address can host a credential — false at a bare IP, where an rpId
 * cannot be a domain and no certificate helps. This asks the CLIENT whether the ceremony can start:
 * WebAuthn is secure-context-only, so over plain http the browser does not expose
 * `PublicKeyCredential` at all and every ceremony fails before it reaches the network. A domain
 * reached over http answers YES to the first question and NO to this one, which is exactly the gap
 * three surfaces fell into.
 *
 * PRESENCE VERSUS AVAILABILITY, which is why this is a bug and not a ruling. Whether this device
 * HOLDS a credential is undetectable, so `PasswordForm` is right to show its button unconditionally
 * and let "no passkey here" be the answer. Availability is detectable in one expression, and where
 * the honest answer is "this connection cannot do passkeys at all", three buttons that fail
 * differently is the *no silent caps or fallbacks* rule read at the level of a control.
 *
 * `isSecureContext` IS NOT CHECKED SEPARATELY, deliberately: it is the CAUSE, and the absence of
 * `PublicKeyCredential` is the EFFECT this code actually depends on. Testing the effect keeps this
 * true for any other reason a browser might withhold the API — an old build, a policy, an embedded
 * view — rather than only for the one we can name.
 */
export function webauthnAvailable(): boolean {
  return typeof window !== "undefined" && typeof window.PublicKeyCredential !== "undefined";
}




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
//
// `currentPassword` IS RULE 1's LIGHTER FACTOR, AND OMITTING IT BROKE THE SHIPPING FLOW. Adding the
// first passkey right after setting a password is a call on an install that is already `configured`
// with a password and NO credentials — so `RequirePresent` reached `verifyPassword("")` and answered
// `bad_password`, and the user was told *"current password is incorrect"* about a field they were
// never shown. Caught in review of quince#930; the password is in scope at that call site two
// statements above it.
//
// THAT SERVER-SIDE HALF IS FIXED SINCE quince#978 — presenting nothing is now `reauth_required`,
// not `bad_password` — but passing the password is still right and still cheaper: it satisfies rule
// 1 on the FIRST `begin`, so first-run setup never meets a challenge it has nothing to answer with.
export async function registerPasskey(
  name: string,
  opts: { firstRun?: boolean; currentPassword?: string; proof?: string; enrolmentSecret?: string } = {},
): Promise<boolean> {
  // THREE CEREMONIES, ONE IMPLEMENTATION — and this function's own history is the argument. The
  // comment at the bottom of this file records that "the registration path already cost a bug that
  // only one of three copies would have carried"; a fourth copy for enrolment would be that bug
  // waiting. What differs between them is the base and one query parameter, so that is all that
  // varies here (qn.13 D4).
  const base = opts.enrolmentSecret
    ? "/api/enrol/passkey"
    : opts.firstRun
      ? "/api/auth/setup/passkey"
      : "/api/auth/passkeys/register";
  // THE SECRET RIDES ON BOTH HALVES, because the server re-reads it at finish rather than trusting
  // begin — the admin can cancel a QR while this phone is sitting on the Face ID sheet.
  //
  // ABSENT MEANS NO QUERY AT ALL, not an empty one. An unconditional `?` changed every existing
  // begin URL from `/begin` to `/begin?`, which the auth tests caught immediately — harmless to a
  // server and wrong in the one place a URL is compared.
  const secretQuery = opts.enrolmentSecret
    ? `secret=${encodeURIComponent(opts.enrolmentSecret)}`
    : "";
  const beginPath = secretQuery ? `${base}/begin?${secretQuery}` : `${base}/begin`;
  // Omitted rather than sent empty when there is none: on a passwordless install an empty string is
  // a WRONG password, where an absent field is the case the server decides for itself.
  const present = {
    ...(opts.currentPassword ? { current_password: opts.currentPassword } : {}),
    // A PROOF THE CALLER ALREADY EARNED, from a challenge it ran itself (qn.6o slice 4). Same
    // omit-rather-than-empty rule as the password above.
    ...(opts.proof ? { proof: opts.proof } : {}),
  };

  // RULE 1: THE SERVER REFUSES AT **BEGIN**, BEFORE A CEREMONY EXISTS — qn.6n slice 5b.
  // `changePassword` can retry its whole call, because nothing was consumed by the refused attempt.
  // A creation ceremony IS consumed, so learning the requirement after `create()` would mean a
  // second Face ID sheet for a credential the user has already made. Refusing at `begin` is what
  // makes the demand recoverable at all.
  //
  // WHAT THIS NO LONGER DOES IS RECOVER BY ITSELF, and removing that is qn.6o slice 4's point.
  // There was a `catch` here that ran `proveWithPasskey` and retried `begin` with the proof. It
  // read as the obvious kindness and it is the exact chain quince#976 is filed on:
  //
  //     click → reauth/begin → credentials.get() → reauth/finish → begin(proof) → create()
  //                            ^ the user's gesture is SPENT on the proof's own sheet
  //
  // COMPLETING AN AUTHENTICATOR SHEET GRANTS NO NEW ACTIVATION — it is browser UI, not a DOM
  // activation-triggering event — so `create()` arrived three awaits and one sheet past the last
  // real click. The caller must instead run the challenge, and then reach this function from a
  // FRESH CLICK, which is the one thing a library cannot do on its own (spec D1, as corrected on
  // quince#988).
  //
  // IT NEVER FIRED IN SHIPPED CODE, which is why deleting it costs nothing: first run is exempt
  // and skips it, the setup page passes `currentPassword` so `begin` succeeds first try, and
  // `AddPasskeyDialog` held its own copy of this ceremony and never called here at all.
  //
  // SO `reauth_required` NOW REACHES THE CALLER. It carries `accepts`, which is what the caller
  // renders the challenge from (slice 2).
  const begin = await api.post<BeginRegistration>(beginPath, present);
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
    `${base}/finish?${secretQuery ? secretQuery + "&" : ""}ceremony=${encodeURIComponent(begin.ceremony)}` +
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
  // THE ID THIS CEREMONY JUST CREATED, so the next sign-in on this browser offers exactly it
  // rather than asking the platform to choose (qn.13 D2.2).
  rememberPasskey(cred.id);
  return true;
}

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
 *
 * `forgetHint` runs the ceremony as if this browser remembered nothing — the *change user* path
 * (qn.13 D2.2). It is deliberately not a separate mode: what it asks for is the discoverable flow,
 * which is what quince did before this rung and what it still does for a browser with no hint.
 */
export async function signInWithPasskey(opts: {
  conditional: boolean;
  signal?: AbortSignal;
  forgetHint?: boolean;
}): Promise<unknown> {
  const hint = opts.forgetHint ? "" : passkeyHintCredentialID();
  try {
    return await assertOnce(opts, hint);
  } catch (err) {
    // A REMEMBERED CREDENTIAL THAT NO LONGER EXISTS MUST FALL BACK, NOT DEAD-END (D2.2, G8). The
    // admin revoked it, or the authenticator dropped it, and `allowCredentials` then names one
    // thing the platform cannot find — so it reports no passkey available and the user is stuck on
    // a page that should have worked.
    //
    // ONLY WHEN A HINT WAS SENT, and only once. With no hint the ceremony was already discoverable,
    // so a retry would run the identical request and turn one refusal into two sheets.
    //
    // AND NOT FOR AN `APIError`, which is the server having REJECTED the assertion rather than the
    // platform failing to produce one. Retrying that is asking a question already answered; the
    // catch below has already forgotten the hint for exactly that case.
    if (hint !== "" && !(err instanceof APIError)) {
      forgetPasskey();
      return await assertOnce(opts, "");
    }
    throw err;
  }
}

/**
 * assertOnce runs one assertion ceremony, offering `hint` when it is non-empty.
 *
 * SPLIT OUT SO THE FALLBACK IS A SECOND CALL RATHER THAN A FLAG. The retry must repeat the whole
 * ceremony — a new `begin`, a new challenge, a new ceremony key — because the first one was spent:
 * the server takes the key single-use, so reusing it answers `no_ceremony`. Writing it as one
 * function with a loop hid that; two calls make it obvious that the second is a full round trip.
 */
async function assertOnce(
  opts: { conditional: boolean; signal?: AbortSignal },
  hint: string,
): Promise<unknown> {
  const begin = await api.post<BeginAssertion>(
    "/api/auth/passkeys/login/begin",
    hint === "" ? {} : { credential_id: hint },
  );
  const pk = begin.options.publicKey;

  const cred = (await navigator.credentials.get({
    ...(opts.conditional ? { mediation: "conditional" as CredentialMediationRequirement } : {}),
    ...(opts.signal ? { signal: opts.signal } : {}),
    publicKey: {
      ...pk,
      challenge: b64urlToBytes(pk.challenge as unknown as string),
      // THE SERVER'S LIST, NOT AN EMPTY ONE. This read `allowCredentials: []` until qn.13 — a
      // hardcoded "platform, you choose" that would have discarded the offer even once the server
      // began sending one, and silently, because an empty list is also the valid discoverable case.
      //
      // The ids arrive base64url in JSON and `navigator.credentials.get` wants bytes, which is the
      // same undoing `challenge` needs one line up.
      allowCredentials: (pk.allowCredentials ?? []).map((c) => ({
        ...c,
        id: b64urlToBytes(c.id as unknown as string),
      })),
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
    // A passkey worked HERE, and now WHICH one. This is the case registration alone would miss: an
    // iCloud-synced credential created on a phone is usable on a Mac that never registered
    // anything, and after this the Mac offers that credential directly.
    //
    // `cred.id` IS THE ASSERTED CREDENTIAL, not the one that was offered — so a discoverable
    // ceremony teaches the browser an id it did not have, which is how the hint becomes specific
    // without anybody choosing an account.
    rememberPasskey(cred.id);
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

