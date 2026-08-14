import { api } from "@/lib/api";
import { b64urlToBytes, bytesToB64url, type BeginAssertion } from "@/lib/webauthnWire";

// RE-AUTHENTICATION — qn.6n, landed in slice 4 and moved to its own module in slice 5b. Proving a PRESENT credential for one credential-changing
// operation, without signing anybody in.
//
// A SEPARATE CEREMONY FROM `signInWithPasskey`, and the reason is the same one D3 gives for the
// endpoints being separate: the login pair is pre-auth in all three exact-path allowlists, and this
// one requires a session. Sharing the function would have meant one call site choosing between two
// endpoint families by a boolean, which is the shape that gets passed the wrong way round.
//
// ALWAYS MODAL, never conditional. The user has just pressed a button that changes their
// credentials; a non-modal request sitting in an autofill dropdown is for a login form nobody has
// committed to yet.
export type ProofOperation = "add_passkey" | "remove_passkey" | "remove_password" | "set_password";

export async function proveWithPasskey(
  operation: ProofOperation,
  target?: string,
): Promise<string> {
  const begin = await api.post<BeginAssertion>("/api/auth/reauth/begin", {
    operation,
    ...(target ? { target } : {}),
  });
  const pk = begin.options.publicKey;

  const cred = (await navigator.credentials.get({
    publicKey: {
      ...pk,
      challenge: b64urlToBytes(pk.challenge as unknown as string),
      // DISCOVERABLE, so the authenticator offers whatever it holds for this address — the same
      // shape login uses, and the reason quince needs no username field anywhere.
      allowCredentials: [],
    },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("no credential");

  const resp = cred.response as AuthenticatorAssertionResponse;
  const out = await api.post<{ proof: string }>(
    `/api/auth/reauth/finish?ceremony=${encodeURIComponent(begin.ceremony)}`,
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
  // NO `rememberPasskey()` HERE, unlike the login ceremony. That hint exists to decide whether to
  // OFFER a passkey at sign-in; someone re-authenticating is already signed in, and recording the
  // fact would conflate "a passkey works here" with "a passkey signed me in here". They are
  // different questions and the sign-in surface asks the second.
  return out.proof;
}

// WHAT THE SERVER SAID WOULD SATISFY A REFUSAL — qn.6o slice 2's `accepts`, read on the client.
//
// HERE RATHER THAN IN A COMPONENT because it is wire-shaped knowledge with two consumers since
// slice 5: the add row and the passkey list. It lived in `AddPasskeyRow.tsx` while there was one.

// A factor the server named. A union of the two strings rather than a boolean pair, so a third
// factor widens this and fails the build at every place that switches on it.
export type Factor = "password" | "passkey";

// What a caller presents once a factor has satisfied the challenge. Exactly one of the two, matching
// the server's `Presented`.
export type Present = { current_password: string } | { proof: string };

// acceptsOf pulls the list off a refusal.
//
// READ OFF `details`, WHICH IS THE WHOLE PARSED BODY, rather than promoted to a field on `APIError`.
// That class carries `code`, `message` and the raw body; adding a typed field for one code's one
// extra key would put a `reauth_required`-shaped hole in a type every error in the product uses.
//
// NARROWED RATHER THAN CAST. The values come off the wire, so a `Factor[]` assertion would be a
// promise this code cannot keep — a daemon sending a factor this build has never heard of would flow
// straight into the challenge and render nothing for it.
//
// UNDEFINED FOR AN ABSENT OR UNRECOGNISABLE LIST, and callers must treat that as *the server did not
// say* rather than as *nothing would work*. The two are different refusals: the server sends
// `last_credential` for a genuine dead end (D4), so an absent list here means an older daemon.
export function acceptsOf(err: { details?: unknown }): Factor[] | undefined {
  const body = err.details;
  if (!body || typeof body !== "object" || !("error" in body)) return undefined;
  const list = (body as { error?: { accepts?: unknown } }).error?.accepts;
  if (!Array.isArray(list)) return undefined;
  const known = list.filter((f): f is Factor => f === "password" || f === "passkey");
  return known.length > 0 ? known : undefined;
}
