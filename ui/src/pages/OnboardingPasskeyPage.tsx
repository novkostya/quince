import * as React from "react";
import { useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { api, APIError } from "@/lib/api";

// The onboarding passkey offer — qn.6k slice 6, story 9. Ruled on quince#657: "offered in
// onboarding; added later in Settings."
//
// SKIPPING IS THE NORMAL PATH, AND THE PAGE HAS TO LOOK LIKE IT. A passkey is an addition and never
// a replacement — the ruling is explicit, and the reason is that a lost phone must not lock the user
// out of their own backups. A step that pressed for one here would be selling the wrong story on the
// screen where the user forms their idea of what quince expects.

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

type Support = { rp_id: string; supported: boolean };

export function OnboardingPasskeyPage() {
  const nav = useNavigate();
  const [support, setSupport] = React.useState<Support | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  const done = React.useCallback(() => nav("/", { replace: true }), [nav]);

  React.useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const s = await api.get<Support>("/api/auth/passkeys");
        if (!cancelled) setSupport({ rp_id: s.rp_id ?? "", supported: s.supported === true });
      } catch {
        // AN UNREACHABLE LIST MEANS "DO NOT OFFER", not an error on the screen after someone has
        // just set their password. The step is optional; a failure here makes it absent.
        if (!cancelled) setSupport({ rp_id: "", supported: false });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function add() {
    setError(null);
    setBusy(true);
    try {
      const name = window.prompt("Name this passkey — the device you are setting it up on:");
      if (!name?.trim()) return;

      const begin = await api.post<{
        ceremony: string;
        options: { publicKey: PublicKeyCredentialCreationOptions };
      }>("/api/auth/passkeys/register/begin", {});
      const pk = begin.options.publicKey;
      const cred = (await navigator.credentials.create({
        publicKey: {
          ...pk,
          challenge: b64urlToBytes(pk.challenge as unknown as string),
          user: { ...pk.user, id: b64urlToBytes(pk.user.id as unknown as string) },
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
      done();
    } catch (err) {
      if (err instanceof Error && err.name === "NotAllowedError") setError(null); // dismissed
      else if (err instanceof APIError) setError(err.message);
      else setError(err instanceof Error ? err.message : "Could not add a passkey.");
    } finally {
      setBusy(false);
    }
  }

  // NOT OFFERED WHERE IT CANNOT WORK — story 4, and here it means skipping the step entirely rather
  // than showing a refusal. On a tier that cannot hold a passkey there is nothing for the user to
  // decide, and a first-run screen explaining a capability they cannot have is worse than no screen.
  React.useEffect(() => {
    if (support && !support.supported) done();
  }, [support, done]);

  if (!support || !support.supported) return null;

  return (
    <div className="flex min-h-dvh items-start justify-center bg-bg pb-6 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(4rem,env(safe-area-inset-top))] text-fg sm:items-center sm:py-6">
      <div className="w-full max-w-sm rounded-card border border-line bg-card p-6">
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-base font-semibold">Sign in with Face ID?</h1>
        <p className="mt-1 text-sm text-muted">
          Add a passkey and you can sign in with Face ID or Touch ID instead of typing your password
          on a phone keyboard.
        </p>
        <p className="mt-3 text-sm text-muted">
          Your password keeps working either way — a passkey is an addition, never a replacement.
        </p>
        {/* THE HAZARD, WHERE THE CREDENTIAL IS CREATED. Ruled explicitly: not only in the docs. */}
        <p className="mt-3 text-xs text-muted">
          A passkey is tied to the address you set it up on —{" "}
          <span className="font-mono">{support.rp_id}</span>. If you later reach quince by a
          different name, you sign in with your password instead.
        </p>

        {error ? <p className="mt-3 text-sm text-danger">{error}</p> : null}

        <div className="mt-5 flex flex-col gap-2">
          <Button type="button" onClick={add} disabled={busy}>
            {busy ? "Working…" : "Add a passkey"}
          </Button>
          {/* SKIP IS A FIRST-CLASS BUTTON, not a link in the corner. It is the normal answer, and
              the user can add one later from Settings whenever they like. */}
          <Button type="button" variant="outline" onClick={done}>
            Not now
          </Button>
        </div>
      </div>
    </div>
  );
}
