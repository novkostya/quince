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
