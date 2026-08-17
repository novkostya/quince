import { SectionHeading } from "@/components/ui/section-heading";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { configKey } from "@/lib/config";
import { healthKey, useInsecureTransportAllowed } from "@/lib/health";

// THE WAY BACK, AND IT IS WHAT MAKES THE BANNER'S CONTROL SAFE TO OFFER — quince#1069.
//
// With the setting off and a plain-http address every door is shut at once: `POST /api/auth/login`
// answers 426, `POST /api/config/insecure-transport` answers 409 to anybody without a session, and
// the first-run confirm on `/onboarding/https` renders only in `needs_setup` — because in
// `needs_login` an unauthenticated control that RELAXES transport is the downgrade primitive
// quince#908 §3 refuses. Without a control behind a session, a reader who turns the setting off and
// then loses that session is editing `config.yml` over ssh: the dead end quince#908 exists to remove.
//
// SO THE REVERSAL LIVES BEHIND A SESSION, WHERE IT IS SAFE. An admin who is still signed in can turn
// it back on here; a stranger on the LAN cannot. That is the same asymmetry the pre-auth window
// rests on, read the other way round.
//
// AND IT CLOSES D12 FOR THIS KEY — *every setting is editable in the UI*. `ConfigEditor` does not
// render `sessions.allow_insecure_transport` and should not: `PUT /api/config` is a full-document
// replace, and a security setting deserves a control that states its cost rather than a checkbox in
// a form of unrelated fields.
export function PlainHTTPSetting() {
  const allowed = useInsecureTransportAllowed();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const qc = useQueryClient();

  async function set(allow: boolean) {
    setBusy(true);
    setFailed(null);
    try {
      await api.post("/api/config/insecure-transport", { allow });
      // BOTH QUERIES, for the reason the banner's copy of this carries in full: the route writes
      // config, and `ConfigEditor`'s draft follows the config query — a stale one means the next
      // Save on this very page ships a full document that puts the setting back (quince#1069,
      // Operator 2026-08-17). This row sits ON the settings surface, so it is the one place where
      // the stale panel is visible AND the stale draft is one button away.
      await Promise.all([
        qc.invalidateQueries({ queryKey: healthKey }),
        qc.invalidateQueries({ queryKey: configKey }),
      ]);
      setOpen(false);
    } catch (e) {
      setFailed(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="mt-8">
      <SectionHeading>Signing in over plain HTTP</SectionHeading>
      <p className="mt-1 max-w-xl text-sm text-muted">
        {allowed
          ? "Allowed. Sign-ins over plain HTTP work on this network, and the sign-in travels in the clear — anyone who can see the traffic can sign in as you."
          : "Not allowed. quince accepts sign-ins over HTTPS, or from the machine it runs on. This is the shipping default."}
      </p>

      {allowed ? (
        // TURNING IT OFF NEEDS NO CONFIRMATION HERE. It tightens, it is reversible from this same
        // row, and the reader is signed in — the cost the banner's dialog names is about the reader
        // being STRANDED, which the presence of this control is what prevents.
        <Button variant="outline" className="mt-3" disabled={busy} onClick={() => void set(false)}>
          {busy ? "…" : "Stop allowing it"}
        </Button>
      ) : (
        // TURNING IT ON IS THE DIRECTION THAT COSTS SOMETHING, so it is the direction that confirms.
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button variant="outline" className="mt-3">
              Allow plain HTTP
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogTitle>Allow sign-in over plain HTTP</DialogTitle>
            <DialogDescription>
              Your sign-in will travel in the clear, so anyone who can see this network&rsquo;s
              traffic can sign in as you. quince will say so on every screen until you turn it off
              again.
            </DialogDescription>
            <div className="mt-4 flex items-center gap-2">
              <Button variant="destructive" disabled={busy} onClick={() => void set(true)}>
                {busy ? "…" : "Allow it"}
              </Button>
              <Button variant="outline" disabled={busy} onClick={() => setOpen(false)}>
                Cancel
              </Button>
            </div>
            {failed ? (
              <p role="alert" className="mt-3 text-sm text-danger">
                {failed}
              </p>
            ) : null}
          </DialogContent>
        </Dialog>
      )}
      {failed && !open ? (
        <p role="alert" className="mt-2 text-sm text-danger">
          {failed}
        </p>
      ) : null}
    </section>
  );
}
