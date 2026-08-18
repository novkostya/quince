import { useState } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { InsecureTransportBanner } from "@/components/InsecureTransportBanner";
import { CertificateApply } from "@/features/onboarding/CertificateApply";
import { api } from "@/lib/api";
import { probeTargetURL, runProbe, type ProbeOutcome } from "@/lib/probe";
import type { CertificateProbe } from "@/lib/types";

// THE CERTIFICATE STEP — quince#908 §5. Slice 4 built both halves of the CHECK; slice 5 added the
// trial, which lives in `CertificateApply` rather than here. Keeping them apart is what let the
// check be reviewed on whether it tells the truth rather than on whether it is safe to turn on —
// and it is worth keeping, because this file is about describing two files and that one is about
// the ten-minute window in which a certificate can lock somebody out.
//
// A ROUTE RATHER THAN AN ACCORDION, and the reason is not taste (§4). This flow is multi-step and
// stateful — paths, a name, two verdicts — and **an accordion is application state with no URL**: a
// user who fills three fields, navigates away and comes back finds it collapsed and empty. A route is
// state the browser already understands, and quince#838 is separately trying to make Back behave.
//
// THE TWO CHECKS ANSWER DIFFERENT QUESTIONS AND NEITHER SUBSTITUTES FOR THE OTHER:
//
//	offline, on the server   are these two files a certificate and its key, in date, covering the
//	                         name? Nothing else in quince asks before a restart.
//	the browser's own probe  can THIS client — the one about to be redirected — reach quince at that
//	                         name? quince's resolver says nothing about the phone.
export function OnboardingCertificatePage() {
  const [certFile, setCertFile] = useState("");
  const [keyFile, setKeyFile] = useState("");
  const [hostname, setHostname] = useState("");
  const [busy, setBusy] = useState(false);
  const [offline, setOffline] = useState<CertificateProbe | null>(null);
  const [errors, setErrors] = useState<string | null>(null);
  const [reach, setReach] = useState<ProbeOutcome | null>(null);

  const canCheck = certFile.trim() !== "" && keyFile.trim() !== "" && !busy;

  function clearResults() {
    // A STALE VERDICT IS WORSE THAN NONE. Leaving the previous answer on screen while the fields say
    // something else invites acting on a result about different files.
    setOffline(null);
    setReach(null);
    setErrors(null);
  }

  async function check() {
    setBusy(true);
    clearResults();
    try {
      const got = await api.post<CertificateProbe>("/api/onboarding/certificate", {
        cert_file: certFile.trim(),
        key_file: keyFile.trim(),
        hostname: hostname.trim(),
      });
      setOffline(got);

      // THE REACHABILITY HALF RUNS ONLY IF THE PAIR IS USABLE AND A NAME WAS GIVEN. Probing a name
      // whose certificate is already known to be expired would ask the user to debug DNS for a
      // certificate that was never going to work.
      const target = probeTargetURL(hostname);
      if (got.outcome === "usable" && target) {
        setReach(await runProbe(target));
      }
    } catch (e) {
      // A 422 names the field; anything else is the server's own sentence. Either way it is shown
      // rather than replaced with "something went wrong".
      setErrors(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="min-h-dvh bg-bg pb-16 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(2.5rem,env(safe-area-inset-top))] text-fg">
      <div className="mx-auto w-full max-w-2xl">
        <InsecureTransportBanner />
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-xl font-semibold tracking-tight">Give quince your own certificate</h1>
        <p className="mt-1 text-sm text-muted">
          quince checks the files before anything changes, and nothing is written to configuration
          until a certificate has proved itself over https.
        </p>

        <div className="mt-6 space-y-4">
          <Field
            id="cert-file"
            label="Certificate file"
            placeholder="/etc/quince/tls/fullchain.pem"
            value={certFile}
            onChange={(v) => {
              setCertFile(v);
              clearResults();
            }}
          />
          <Field
            id="key-file"
            label="Key file"
            placeholder="/etc/quince/tls/privkey.pem"
            value={keyFile}
            onChange={(v) => {
              setKeyFile(v);
              clearResults();
            }}
          />
          {/* IT STARTS EMPTY AND IS NEVER PRE-FILLED FROM THE CURRENT ADDRESS (§5). That is the name
              they are LEAVING — an IP or a `.local` — and no CA issues for either. Moving them off it
              is the entire point of the step.

              THE LABEL SAYS WHAT EMPTY MEANS, WHICH IS NOT THE SAME AS SAYING IT IS OPTIONAL. It read
              *(optional for now)* — a note about a later step, from which a reader cannot tell that
              leaving it blank aims the whole trial at the address they are on. Optional is how it
              behaves; *keep using this address* is what it does. */}
          <Field
            id="hostname"
            label="The name you will reach quince at"
            hint="Leave it empty to keep using the address you are on now."
            placeholder="quince.example.com"
            value={hostname}
            onChange={(v) => {
              setHostname(v);
              clearResults();
            }}
          />
        </div>

        <div className="mt-4">
          <Button onClick={() => void check()} disabled={!canCheck}>
            {busy ? "Checking…" : "Check these files"}
          </Button>
        </div>

        {errors ? (
          <div role="alert" className="mt-4 rounded-card border border-danger bg-card px-3 py-2 text-sm">
            {errors}
          </div>
        ) : null}

        {offline ? <OfflineResult probe={offline} /> : null}
        {reach ? <ReachResult outcome={reach} /> : null}

        {/* THE TRIAL IS OFFERED ONLY FOR A PAIR THE SERVER CALLED `usable`, and the server refuses
            anything else anyway — it re-runs the same check rather than trusting this page, because
            the files can move between the two calls. Offering the button for a pair that will be
            refused would be a promise the product does not keep. */}
        {offline?.outcome === "usable" ? (
          <CertificateApply
            certFile={certFile.trim()}
            keyFile={keyFile.trim()}
            hostname={hostname.trim()}
            // FROM THE PROBE, NOT FROM THE BROWSER. `window.location.hostname` is the same string
            // most of the time and is the wrong source: the coverage answer beside it came from the
            // daemon comparing the leaf to the host IT saw, and a page deciding on its own would be a
            // second implementation of one question — able to disagree with the sentence directly
            // above the button.
            currentHost={offline.current_host}
            currentHostCovered={offline.current_host_covered}
          />
        ) : offline ? (
          <p className="mt-6 text-sm text-muted">
            Nothing has been saved. Fix what the check reported and run it again — quince serves a
            certificate only once these files pass.
          </p>
        ) : null}

        <p className="mt-6 text-sm">
          <Link className="underline" to="/onboarding/https">
            Back to the other options
          </Link>
        </p>
      </div>
    </div>
  );
}

function Field({
  id,
  label,
  hint,
  placeholder,
  value,
  onChange,
}: {
  id: string;
  label: string;
  // A SENTENCE UNDER THE LABEL, for the one field whose EMPTY state does something. Kept out of the
  // label itself so the accessible name stays the name of the thing.
  hint?: string;
  placeholder: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div>
      <label className="block text-sm" htmlFor={id}>
        {label}
      </label>
      {hint ? (
        <p id={`${id}-hint`} className="mt-0.5 text-xs text-muted">
          {hint}
        </p>
      ) : null}
      <input
        id={id}
        aria-describedby={hint ? `${id}-hint` : undefined}
        className="mt-1 w-full rounded-card border border-line bg-bg px-3 py-2 font-mono text-sm text-fg"
        placeholder={placeholder}
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}

// THE SERVER'S SENTENCE IS SHOWN, NOT REPLACED (quince#514, and quince#940's whole sweep). quince
// knows which of the two files failed and which names the certificate carries; a client composing its
// own prose from the enum cannot, and would say "certificate problem" where the server said
// "/etc/quince/tls/privkey.pem: private key does not match".
function OfflineResult({ probe }: { probe: CertificateProbe }) {
  const ok = probe.outcome === "usable";
  // THE LIST IS READ THROUGH A FALLBACK EVEN THOUGH THE TYPE PROMISES ONE. A field this page reads
  // structurally — `.length`, `.join` — takes the whole flow down with it if the wire ever disagrees
  // with the declared type, and this page has no error boundary above it: the user lands on
  // react-router's default screen with a minified stack and no way back to the step they were on.
  // The cost of not trusting it here is four characters.
  const names = probe.names ?? [];
  return (
    <div
      role="status"
      className={`mt-4 rounded-card border bg-card px-3 py-2 text-sm ${ok ? "border-ok" : "border-danger"}`}
    >
      <div className="flex items-center gap-2">
        <Badge tone={ok ? "ok" : "warn"}>{ok ? "The files are usable" : "Not usable"}</Badge>
      </div>
      <p className="mt-2">{probe.reason}</p>

      {names.length > 0 ? (
        <p className="mt-2 text-xs text-muted">
          Covers: <code className="font-mono">{names.join(", ")}</code>
        </p>
      ) : null}

      {/* REPORTED, NOT JUDGED. A leaf without its intermediate validates on a machine that happens to
          cache the issuer and fails on a phone that does not — the hardest TLS failure to diagnose —
          but whether it matters depends on the issuer, so this states the fact and its consequence
          rather than calling it an error. */}
      {ok && probe.chain_length === 1 ? (
        <p className="mt-2 text-xs">
          This file holds one certificate and no intermediate. That is fine for a self-signed
          certificate; from a CA it often means phones reject it while this computer accepts it.
        </p>
      ) : null}

      {/* WHAT LEAVING THE NAME EMPTY WILL MEAN, said while the user can still act on it. Empty means
          *keep using the address I am on*, so the only thing that decides whether that is a good
          idea is whether this certificate covers that address — a comparison the daemon makes and
          nothing used to report.

          ONLY WHILE THE FIELD IS EMPTY. Once a name is typed, the address in play is that name and
          `outcome` already answers coverage for it; saying anything about the address they are
          leaving would be noise about a place they are on their way out of. */}
      {ok && probe.hostname === "" && probe.current_host !== "" ? (
        probe.current_host_covered ? (
          <p className="mt-2 text-xs text-muted">
            You are on <code className="font-mono">{probe.current_host}</code>, which this
            certificate covers — leaving the name empty keeps you there.
          </p>
        ) : (
          <p className="mt-2 text-xs">
            You are on <code className="font-mono">{probe.current_host}</code>, which this
            certificate does <strong>not</strong> cover. Leaving the name empty keeps you there, and
            your browser will warn you about the certificate every time. Type a name it covers to
            move to one instead.
          </p>
        )
      ) : null}

      {probe.not_after !== "" ? (
        <p className="mt-2 text-xs text-muted">Valid until {probe.not_after}</p>
      ) : null}
    </div>
  );
}

// THE SECOND HALF, AND ITS WORDING RULE IS §6's: say what THIS client could not do, never "DNS is
// wrong". A browser cannot distinguish a name that does not resolve, a refused connection, an
// untrusted certificate and a CORS refusal, so naming one would invent a cause.
function ReachResult({ outcome }: { outcome: ProbeOutcome }) {
  switch (outcome.kind) {
    case "ready":
    case "quince-tls":
      return (
        <div role="status" className="mt-3 rounded-card border border-ok bg-card px-3 py-2 text-sm">
          <strong>quince reached itself at {outcome.url}.</strong> This browser can get there, and it
          was this quince answering.
        </div>
      );
    case "no-forwarded-proto":
      return (
        <div role="status" className="mt-3 rounded-card border border-warn bg-card px-3 py-2 text-sm">
          <strong>Something reached quince at {outcome.url} over plain HTTP.</strong> That name is
          already served by a proxy which is not forwarding the scheme — worth settling before
          quince starts serving TLS at it too.
        </div>
      );
    case "other-quince":
      return (
        <div role="alert" className="mt-3 rounded-card border border-danger bg-card px-3 py-2 text-sm">
          <strong>A different quince answered at {outcome.url}.</strong> The certificate is fine; the
          name points somewhere else.
        </div>
      );
    case "unreachable":
      return (
        <div role="status" className="mt-3 rounded-card border border-warn bg-card px-3 py-2 text-sm">
          <strong>This browser could not reach quince at {outcome.url} yet.</strong> That is expected
          if quince is not serving TLS there — it will be once this is applied. It also covers a name
          that does not resolve here, so open it in a tab if you want the specific answer.
        </div>
      );
  }
}
