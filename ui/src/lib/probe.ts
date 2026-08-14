import { api } from "./api";
import type { ProbeNonce, ProbeResult } from "./types";

// THE REVERSE-PROXY PROBE (quince#939, Operator ruling 2026-08-14).
//
// A user who puts quince behind a TLS-terminating proxy and forgets one nginx directive gets a
// working HTTPS site, a quince that says "Not encrypted", and nothing telling them why. This asks the
// question from the CLIENT's side and reports what quince saw.
//
// BROWSER-SIDE, NEVER SERVER-SIDE. Having quince fetch a user-supplied URL would be an unauthenticated
// SSRF primitive on a LAN appliance — the page is outside every guard — and it would prove the wrong
// thing anyway: what matters is that THIS client, the one about to be redirected, can reach that name.

// ProbeOutcome is the whole result space, as a discriminated union rather than a bag of booleans, so
// the copy for each case cannot drift out of step with the case itself.
export type ProbeOutcome =
  | { kind: "unreachable"; url: string }
  | { kind: "other-quince"; url: string }
  | { kind: "no-forwarded-proto"; url: string }
  | { kind: "quince-tls"; url: string }
  | { kind: "ready"; url: string };

// probeTargetURL turns what a person types into the URL to probe, or null if it cannot.
//
// FORGIVING ABOUT THE SCHEME, because the field asks for a name and people paste addresses. `https`
// is forced: the whole point is to check the encrypted door, so probing `http://` would answer a
// question nobody asked and could report success for a setup that is not encrypted at all.
//
// A PORT SURVIVES IF TYPED. A proxy is usually on 443, but one on 8443 is ordinary and a probe that
// silently dropped the port would report "unreachable" about a working deployment.
export function probeTargetURL(input: string): URL | null {
  const trimmed = input.trim();
  if (trimmed === "") return null;
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
  let parsed: URL;
  try {
    parsed = new URL(withScheme);
  } catch {
    return null;
  }
  if (parsed.hostname === "") return null;
  parsed.protocol = "https:";
  parsed.pathname = "/api/onboarding/probe";
  parsed.hash = "";
  return parsed;
}

// runProbe mints a nonce SAME-ORIGIN, then asks the typed name for it back.
//
// THE NONCE IS WHAT MAKES A SUCCESS MEAN ANYTHING. Without it, an answer proves "a quince responded",
// not "I reached myself" — and if that name points at a different quince on the LAN, the check passes
// and the redirect sends the user somewhere else entirely (quince#908 §5).
//
// EVERY FAILURE MODE COLLAPSES TO `unreachable`, AND THAT IS HONEST RATHER THAN LAZY. A DNS failure, a
// refused connection, an untrusted certificate and a CORS refusal are indistinguishable to a browser
// by design — `fetch` rejects with an opaque TypeError for all of them, and the page must not invent a
// cause it cannot see. The copy therefore says what was tried, not what went wrong.
export async function runProbe(target: URL): Promise<ProbeOutcome> {
  const shown = target.origin;

  const { nonce } = await api.get<ProbeNonce>("/api/onboarding/probe/nonce");

  const url = new URL(target.toString());
  url.searchParams.set("nonce", nonce);

  let answer: ProbeResult;
  try {
    const res = await fetch(url.toString(), {
      // NOT `no-cors`. An opaque response restores the exact ambiguity the nonce exists to remove —
      // it would resolve successfully while being unreadable, so "a quince answered" and "anything
      // at all answered" would look the same (quince#908 §5).
      mode: "cors",
      // NO CREDENTIALS. The endpoint is unauthenticated and the server never sends
      // Allow-Credentials, so asking for them would fail the CORS check outright.
      credentials: "omit",
      cache: "no-store",
    });
    if (!res.ok) return { kind: "unreachable", url: shown };
    answer = (await res.json()) as ProbeResult;
  } catch {
    return { kind: "unreachable", url: shown };
  }

  // THE NONCE IS CHECKED BEFORE `detected` IS BELIEVED. `detected` describes the connection of
  // whoever answered, so reading it from a stranger would be reporting a different box's transport
  // as this one's.
  if (answer.nonce !== nonce) return { kind: "other-quince", url: shown };

  switch (answer.detected) {
    case "forwarded_proto":
      return { kind: "ready", url: shown };
    case "tls":
      return { kind: "quince-tls", url: shown };
    default:
      return { kind: "no-forwarded-proto", url: shown };
  }
}
