import { useState } from "react";
import { Link } from "react-router-dom";
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
  const [done, setDone] = useState(false);
  const qc = useQueryClient();

  async function allow() {
    setBusy(true);
    setFailed(null);
    try {
      await api.post("/api/config/insecure-transport", { allow: true });
      // quince#446 requires this degraded mode be surfaced in three channels and quince#539 shipped
      // the non-dismissible banner; invalidating health is what makes it appear immediately rather
      // than at the next poll.
      //
      // THE BANNER IS NOT THE RECEIPT, WHICH IS WHAT THIS COMMENT USED TO CLAIM (quince#1064). It
      // mounts at the TOP of `OnboardingHTTPSPage`, above the wordmark; this confirm sits below the
      // whole tier chooser. On a phone the two are several screens apart, so the user pressed a
      // button, everything within the viewport stayed identical, and the only honest reading was
      // that nothing had happened. Measured on a rig: the write succeeded every time.
      await qc.invalidateQueries({ queryKey: healthKey });
      setDone(true);
    } catch (e) {
      setFailed(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // THE SUCCESS STATE, AND IT IS WHERE THE FINGER ALREADY IS. The failure paths below render a
  // `role="alert"` at the button; success rendered nothing at all, so the one path that WORKS was
  // the only one with no feedback where the act happened.
  //
  // STILL NO AUTOMATIC NAVIGATION, and now for a mechanical reason as well as the original one.
  // `SetupGate` diverts on `insecure_origin`, which it reads from the cached health query — navigate
  // before that refetch has landed and the gate sends the user straight back here, which reads as a
  // loop. A link they press themselves cannot race anything.
  //
  // IT DOES NOT REPEAT THAT THE WARNING WILL PERSIST — Operator direction, 2026-08-16. The confirm
  // one screen up says it before the press, which is where it changes a decision; saying it again
  // afterwards tells somebody who has already chosen something they cannot act on.
  //
  // THE LINK GOES TO `/`, NOT TO `/setup` — Operator direction, same day, and the sibling change on
  // quince#1070 carries the full reasoning. `RequireAuth` routes on the LIVE auth state, so the root
  // resolves to setup on first run and to login on a claimed install; naming the step here would
  // hardcode an order this component has no business knowing. The LABEL still names it, and can:
  // this state only exists in the pre-credential window, where `POST /api/config/insecure-transport`
  // is reachable at all — anywhere else the route answers 409 and this never renders.
  if (done) {
    return (
      <div role="status" className="mt-3 rounded-card border border-warn bg-card px-3 py-2">
        <p className="text-sm">
          <strong>Plain HTTP is on.</strong>
        </p>
        <Button asChild className="mt-3">
          <Link to="/">Set your password</Link>
        </Button>
      </div>
    );
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
