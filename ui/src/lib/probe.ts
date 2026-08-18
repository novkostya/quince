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

// reachTargetURL builds the probe URL for the CERTIFICATE step, and it differs from
// `probeTargetURL` in the one way that matters: it asks over **http**, on the port this page is
// already served from.
//
// PROBING https BEFORE A CERTIFICATE EXISTS ASKS A QUESTION WITH A KNOWN ANSWER. quince is not
// serving TLS at that name yet — that is the entire reason the user is on this screen — so the probe
// fails, and the failure is indistinguishable from the one that matters: a name that does not resolve
// here, or resolves somewhere else. One result, two opposite meanings, and the copy had to hedge.
//
// quince IS SERVING http AT THAT NAME RIGHT NOW, IF THE NAME REACHES IT. So this asks the question the
// trial actually depends on — *does this name reach THIS quince from THIS browser* — and answers it
// before a trial is spent finding out. What it cannot answer is whether the browser will trust
// the issuer, and no probe can.
//
// THE PORT COMES FROM THE PAGE unless the user typed one. quince serves both protocols on ONE
// listener, so the name will be reached on the port they are on now; defaulting to 80 would report
// "unreachable" about a working deployment on :8969.
//
// SAME-SCHEME, WHICH IS WHY THIS IS ALLOWED AT ALL. The page is on http, so fetching http is not
// mixed content. From an https page a browser would block it — and from there this whole tier is
// unnecessary.
export function reachTargetURL(input: string, currentPort: string): URL | null {
  const target = probeTargetURL(input);
  if (target === null) return null;
  target.protocol = "http:";
  if (!/:\d+\s*$/.test(input.trim())) target.port = currentPort;
  return target;
}

// reachedThisQuince reports whether the probe got its own nonce back, whatever the connection it
// found there looked like.
//
// THE THREE "SUCCESS" KINDS ALL MEAN THE NONCE MATCHED — they differ only in what `detected` said
// about the answering connection, which is tier 1's question (is a proxy forwarding the scheme) and
// not this one's. Here the whole question is *did I reach myself*.
export function reachedThisQuince(outcome: ProbeOutcome): boolean {
  return outcome.kind === "ready" || outcome.kind === "quince-tls" || outcome.kind === "no-forwarded-proto";
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
