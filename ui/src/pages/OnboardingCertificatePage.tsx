import { useState } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { InsecureTransportBanner } from "@/components/InsecureTransportBanner";
import { CertificateApply } from "@/features/onboarding/CertificateApply";
import { api } from "@/lib/api";
import { reachTargetURL, reachedThisQuince, runProbe, type ProbeOutcome } from "@/lib/probe";
import type { CertificateProbe } from "@/lib/types";

// THE CERTIFICATE STEP — quince#908 §5. Slice 4 built both halves of the CHECK; slice 5 added the
// trial, which lives in `CertificateApply` rather than here. Keeping them apart is what let the
// check be reviewed on whether it tells the truth rather than on whether it is safe to turn on —
// and it is worth keeping, because this file is about describing two files and that one is about
// the window in which a certificate can lock somebody out.
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
      // THE REACHABILITY HALF RUNS ONLY IF THE PAIR IS USABLE AND A NAME WAS GIVEN. Probing a name
      // whose certificate is already known to be expired would ask the user to debug DNS for a
      // certificate that was never going to work.
      //
      // OVER http, AT THIS PAGE'S PORT — see `reachTargetURL`. quince cannot be serving TLS at that
      // name yet, so probing https would fail by construction and say nothing; it IS serving http
      // there if the name reaches it at all, which is exactly the precondition the trial needs.
      const target = reachTargetURL(hostname, window.location.port);
      let outcome: ProbeOutcome | null = null;
      if (got.outcome === "usable" && target) {
        // ITS OWN CATCH, so a probe that cannot even start — the nonce mint failing — does not throw
        // away a verdict about the FILES that already arrived and is still worth showing.
        try {
          outcome = await runProbe(target);
        } catch {
          outcome = { kind: "unreachable", url: target.origin };
        }
      }

      // BOTH RESULTS, ONE RENDER, AND THAT IS THE WHOLE POINT OF THE ORDER HERE. Setting the verdict
      // as soon as it arrives showed the trial card with a LIVE button, then a moment later inserted
      // the reachability refusal above it and disabled the button — a screen that offered an action
      // and withdrew it while the user was looking at it. React batches these two, so the results
      // appear together or not at all.
      //
      // THE COST IS A LONGER WAIT WITH NOTHING ON SCREEN, and it is paid deliberately: the button
      // says "Checking…" throughout, and a slow answer is better than a wrong one that corrects
      // itself.
      setOffline(got);
      setReach(outcome);
    } catch (e) {
      // A 422 names the field; anything else is the server's own sentence. Either way it is shown
      // rather than replaced with "something went wrong".
      setErrors(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // WHETHER A TRIAL WOULD BE POINTLESS. Two independent facts make it so and they come from two
  // different checks — which is exactly how the second one shipped unguarded: a name the certificate
  // covered, unreachable from this browser, with the button live under a red box saying so.
  //
  // NEITHER IS A JUDGEMENT ABOUT THE FILES. The pair is `usable` in both cases; what is wrong is
  // where it would be tried. And neither blocks the journey the Operator ruled valid — a browser
  // warning about the ISSUER, on an address that works — because that address is covered and
  // reachable.
  //
  // THE REASON IS NOT PASSED DOWN, only the verdict: both causes are already stated by the checks
  // above the card, and repeating one inside it put the same sentence on screen twice.
  const blocked: boolean = (() => {
    if (offline === null || offline.outcome !== "usable") return false;
    // THE CERTIFICATE CANNOT COVER WHERE THE LINK WOULD POINT. Only with the name left empty: with
    // one typed, the address in play is that name and `outcome` has already answered for it.
    if (hostname.trim() === "" && offline.current_host !== "" && !offline.current_host_covered) return true;
    // OR THIS DEVICE CANNOT GET THERE AT ALL, which the confirmation link would need to.
    if (reach !== null && !reachedThisQuince(reach)) return true;
    return false;
  })();

  return (
    <div className="min-h-dvh bg-bg pb-16 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(2.5rem,env(safe-area-inset-top))] text-fg">
      <div className="mx-auto w-full max-w-2xl">
        <InsecureTransportBanner />
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-xl font-semibold tracking-tight">Give quince your own certificate</h1>
        <p className="mt-1 text-sm text-muted">
          quince checks the files first. Nothing is saved until the certificate has worked over
          https.
        </p>

        {/* THE PLACEHOLDERS ARE THE PATHS THE SHIPPED EXAMPLES PRODUCE, and that is the whole
            requirement they have to meet. `deploy/compose.yml` and
            `deploy/compose.host-muxer.yml` both carry `./quince/certs:/certs:ro`, so a certificate
            dropped in beside them lands under `/certs/`. A placeholder from a different convention
            teaches a path that does not exist on any install this project describes — worse than an
            empty box, because it reads as instruction.

            `deploy/tls.md` NO LONGER CARRIES A `tls:` BLOCK TO COPY, and this comment cited one
            until quince#1307: the documented route is now mount-then-onboard, which is this screen.
            So these placeholders are the only place those paths are stated to a user, which raises
            rather than lowers the bar they have to meet. */}
        {/* A FORM, SO RETURN SUBMITS IT. Three text fields and one action is a form whatever the
            markup says, and a keyboard user pressing Return on the last field expects the button —
            on a phone the key is literally labelled "go". Without this the page silently does
            nothing, which is the third time this defect has been reported on this flow. */}
        <form
          className="mt-6 space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (canCheck) void check();
          }}
        >
          <Field
            id="cert-file"
            label="Certificate file"
            placeholder="/certs/quince.pem"
            value={certFile}
            onChange={(v) => {
              setCertFile(v);
              clearResults();
            }}
          />
          <Field
            id="key-file"
            label="Key file"
            placeholder="/certs/quince.key"
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

          <div className="mt-4">
            {/* THE DEFAULT BUTTON, which is what makes Return work — and being disabled is what makes
                Return correctly do nothing while a field is empty, with no second rule to keep in
                step. */}
            <Button type="submit" disabled={!canCheck}>
              {busy ? "Checking…" : "Check these files"}
            </Button>
          </div>
        </form>

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
            // THE REASON IS COMPOSED HERE, from the daemon's own coverage answer and this
            // browser's own reach result — see `blocked` above.
            blocked={blocked}
          />
        ) : offline ? (
          <p className="mt-6 text-sm text-muted">
            Nothing was saved. Fix what the check reported and try again.
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
// "/certs/quince.key: private key does not match".
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
            It covers <code className="font-mono">{probe.current_host}</code>, the address you are
            on, so you can leave the name empty.
          </p>
        ) : (
          <p className="mt-2 text-xs">
            It does <strong>not</strong> cover <code className="font-mono">{probe.current_host}</code>,
            the address you are on. Leave the name empty and your browser will warn you every visit.
          </p>
        )
      ) : null}

      {probe.not_after !== "" ? (
        <p className="mt-2 text-xs text-muted">Valid until {probe.not_after}</p>
      ) : null}
    </div>
  );
}

// THE SECOND HALF, AND IT NOW ASKS A QUESTION WITH AN UNKNOWN ANSWER.
//
// It probes **http** at that name (see `reachTargetURL`), which is what quince is serving while this
// page is open. So a failure here is a real finding rather than the foregone one: probing https
// before a certificate exists fails by construction, and the old copy had to hedge — *"that is
// expected … it also covers a name that does not resolve here"* — one sentence for the harmless case
// and the fatal one.
//
// THE WORDING RULE IS UNCHANGED (§6): say what THIS client could not do, never "DNS is wrong". A
// browser cannot tell a name that does not resolve from a refused connection from a CORS refusal, so
// naming a cause would be inventing one.
//
// WHAT IT STILL CANNOT SAY is whether the browser will trust the ISSUER once TLS is up. No probe can
// — that is what the trial is for, and why a good answer here is a precondition rather than a
// promise.
function ReachResult({ outcome }: { outcome: ProbeOutcome }) {
  if (reachedThisQuince(outcome)) {
    return (
      <div role="status" className="mt-3 rounded-card border border-ok bg-card px-3 py-2 text-sm">
        <strong>That name reaches quince.</strong> Trying the certificate will move it to https.
      </div>
    );
  }
  if (outcome.kind === "other-quince") {
    return (
      <div role="alert" className="mt-3 rounded-card border border-danger bg-card px-3 py-2 text-sm">
        <strong>Something else answers at {outcome.url}.</strong> That name does not point at this
        quince.
      </div>
    );
  }
  return (
    <div role="alert" className="mt-3 rounded-card border border-danger bg-card px-3 py-2 text-sm">
      <strong>This device cannot reach quince at {outcome.url}.</strong> Check that the name points
      at this machine.
    </div>
  );
}
