import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { healthKey } from "@/lib/health";

// THE PLAIN-HTTP TIER'S AFFORDANCE — quince#908 §4: *one boolean and a warning, a CONFIRM, not a
// page*. It is the escape hatch for a first-run user who cannot otherwise finish setup at all.
//
// TWO STEPS, DELIBERATELY. The first press reveals what is being given up; the second does it.
// Everything else on this page is reversible by navigating away, and this is not — it changes the
// daemon's configuration, and it is the one option whose whole cost is a thing that will not be
// visible afterwards.
//
// FIRST RUN ONLY, and the SERVER enforces it rather than this component: `POST
// /api/config/insecure-transport` is gated on `auth.Configured()` and answers 409 the instant the
// install is claimed (contracts §1). quince#908 §3 is explicit that in `needs_login` the same
// control would be a real downgrade primitive — flip it, wait for the admin to sign in over plain
// http, read the cookie — so this being hidden is presentation, and the 409 is the control.
export function PlainHTTPConfirm() {
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const qc = useQueryClient();

  async function allow() {
    setBusy(true);
    setFailed(null);
    try {
      await api.post("/api/config/insecure-transport", { allow: true });
      // THE BANNER IS THE RECEIPT. quince#446 requires this degraded mode be surfaced in three
      // channels and quince#539 shipped the non-dismissible one; invalidating health is what makes
      // it appear immediately rather than at the next poll. The user should watch the warning
      // arrive as a consequence of what they just did, not meet it later as a surprise.
      await qc.invalidateQueries({ queryKey: healthKey });
      // NO NAVIGATION. `SetupGate` sends a first-run visitor here while the cookie would be
      // discarded; with the opt-in on `insecure_origin` goes false, so the ordinary route works
      // again and the link below is a plain one. Pushing them somewhere automatically would take
      // the decision away at the exact moment they should see its consequence.
    } catch (e) {
      setFailed(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!armed) {
    return (
      <div className="mt-3">
        <Button variant="outline" onClick={() => setArmed(true)}>
          Allow plain HTTP on this network
        </Button>
      </div>
    );
  }

  return (
    <div role="group" className="mt-3 rounded-card border border-warn bg-card px-3 py-2">
      {/* THE COST RESTATED AT THE MOMENT OF DECIDING, not left three paragraphs up. The card above
          explains the trade to somebody reading; this is what somebody about to press a button
          reads, and it is the shortest true version of it. */}
      <p className="text-sm">
        <strong>Your sign-in cookie will cross this network unencrypted.</strong> Anyone who can see
        the traffic can sign in as you. quince will keep saying so on every screen until you turn it
        off.
      </p>
      <div className="mt-3 flex items-center gap-2">
        <Button variant="destructive" onClick={() => void allow()} disabled={busy}>
          {busy ? "Turning it on…" : "Turn it on"}
        </Button>
        <Button variant="outline" onClick={() => setArmed(false)} disabled={busy}>
          Cancel
        </Button>
      </div>
      {failed ? (
        // THE SERVER'S OWN SENTENCE. A 409 here means the install was claimed between loading this
        // page and pressing the button, and "quince is already set up" is exactly what the user
        // needs to read — replacing it with "could not save" would hide the one useful fact.
        <p role="alert" className="mt-2 text-sm text-danger">
          {failed}
        </p>
      ) : null}
    </div>
  );
}
