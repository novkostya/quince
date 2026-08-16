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

// WHILE `sessions.allow_insecure_transport` IS ON, EVERY SCREEN SAYS SO — quince#446's third
// channel, filed as quince#539, and required by the Operator's ruling on quince#908 slice 6.
//
// THE RULING IS WHAT MAKES THIS LOAD-BEARING RATHER THAN A NICETY. Slice 6c adds a PRE-AUTH route
// that can turn this setting on, deliberately, so a stranded first-run user has an exit. That means
// the setting can be enabled by somebody who is not the owner — and the owner then arrives at a
// login form, over plain http, about to type a password that will cross the network in clear. This
// is the only thing standing between them and that.
//
// NON-DISMISSIBLE, ruled explicitly (quince#446): no close button, no `localStorage` "don't show
// again", no timeout. A degraded mode that can be hidden stops being surfaced. It goes away when the
// setting does, and only then.
//
// IT NAMES WHAT IS UNPROTECTED, not merely that something is. "Insecure connection" is a status; the
// session cookie and the CSRF token crossing the network in clear is the thing a reader can weigh —
// and the consequence is that anyone who can read the path can sign in as the admin of an
// application holding a person's entire digital life.
//
// `role="alert"`, UNLIKE `ReconcilingNotice`'s `role="status"`. That one reports a list still
// filling in and should be heard in turn; this one reports a standing security condition, and a
// screen-reader user deciding whether to type a password is exactly who should be interrupted.
//
// KEYED ON `insecure_transport_allowed`, AND NOT ON `insecure_origin`. They are inverses on the
// install that matters: with the opt-in on nothing is discarded, so the neighbouring field reads
// `false` here. The hook carries the same warning, because this is a one-line change somebody will
// be tempted to make.
export function InsecureTransportBanner() {
  // LOCAL STATE, NOT `useDialogRoute`, AND THIS IS THE ONE PLACE THAT EXCEPTION IS RIGHT. quince#931
  // routes destructive confirms so Back dismisses them — correct for a dialog belonging to a page.
  // This one belongs to a BANNER that renders in all three shells, including the pre-auth ones, so a
  // routed dialog would hang a query param off every route in the product, `/login` included.
  //
  // AND IT WOULD COST MORE THAN A QUERY PARAM: `useDialogRoute` and `useNavigate` both require a
  // Router, so routing this dialog imposes one on every surface that renders the banner — including
  // ones whose tests mount it bare. A global banner should not be able to do that to the app.
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const qc = useQueryClient();
  const allowed = useInsecureTransportAllowed();

  async function turnOff() {
    setBusy(true);
    setFailed(null);
    try {
      await api.post("/api/config/insecure-transport", { allow: false });

      // THE BANNER REMOVES ITSELF, because it renders off `insecure_transport_allowed` — so
      // invalidating health IS the receipt, and nothing has to be reloaded to see it happen.
      //
      // AND THE CONFIG QUERY, WHICH IS A SEPARATE ONE. The visible cost of missing it is a stale
      // *Current configuration* panel on Settings; the expensive one is that `ConfigEditor`'s draft
      // follows that query (quince#764) and `PUT /api/config` is a FULL-DOCUMENT REPLACE — so a
      // reader who changes this here and then saves an unrelated field on the same screen ships a
      // document carrying the OLD value and silently puts the setting back.
      //
      // BOTH KEYS, EVERY TIME THIS ROUTE IS CALLED. It writes config, so config is stale; health
      // derives from it, so health is stale too. One without the other is a screen disagreeing with
      // itself.
      await Promise.all([
        qc.invalidateQueries({ queryKey: healthKey }),
        qc.invalidateQueries({ queryKey: configKey }),
      ]);

      // IT MUST NOT SIGN THE READER OUT — Operator ruling, 2026-08-16 (quince#1069), and the reason
      // is a LOCKOUT rather than a preference. With the setting off and a plain-http address:
      // `POST /api/auth/login` answers 426; this route answers 409 to anybody without a session; and
      // the first-run confirm on `/onboarding/https` does not render in `needs_login`, because an
      // unauthenticated control that RELAXES transport is the downgrade primitive quince#908 §3
      // refuses. Ending the session leaves ssh and a text editor as the only way back — the dead end
      // quince#908 exists to remove.
      //
      // SO THE SESSION STAYS. The setting governs sign-ins from here on; the reader keeps the one
      // they hold, and `PlainHTTPSetting` on Settings → Sign-in is the reversal they can still reach.
      // Whether OTHER live sessions should end is quince#1080's question and the server's to answer:
      // a client that signs ITSELF out fixes nothing there and strands the one person who was
      // tightening the install.
      setOpen(false);
    } catch (e) {
      setFailed(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // RENDERS NOTHING WHEN OFF, which is the shipping default and almost every install. A permanent
  // element that merely changed wording would train people to stop reading it.
  if (!allowed) return null;
  return (
    <div
      role="alert"
      // THE DANGER TOKENS, not the muted card `ReconcilingNotice` uses. That notice says "wait a
      // moment"; this one says "your password is about to cross the network in clear", and the two
      // must not look alike at a glance. `border-danger`/`text-danger` are house tokens — see
      // `index.css`; the pale-slab failure that hit the sibling component came from reaching for
      // shadcn defaults that Tailwind then emitted nothing for.
      className="mb-4 rounded-card border border-danger bg-card px-3 py-2 text-sm text-fg"
    >
      {/* WRITTEN FOR THE PERSON READING IT — Operator direction, 2026-08-16 (quince#1069). No config
          key and no mechanism: a key is not a sentence, and a CSRF token is a detail nobody outside
          this codebase can act on.

          IT STILL NAMES WHAT IS UNPROTECTED, which is quince#446's ruling. "Your sign-in travels in
          the clear" is that claim in words a person can weigh; the cookie and the token are HOW, and
          how is not what somebody about to type a password needs in order to decide anything.

          IT IS ABOUT THE INSTALL, NOT THIS CONNECTION. The setting is global, so a reader can be on a
          perfectly good HTTPS address while it is on — "this connection is not encrypted" would be a
          lie to exactly that reader. */}
      <strong className="text-danger">quince is allowing plain HTTP.</strong> Your sign-in travels in
      the clear, so anyone who can see the traffic can sign in as you.{" "}
      {/* THE INSTRUCTION IS THE CONTROL — Operator direction, 2026-08-16, correcting the first
          attempt at quince#1069, which put a separate button under the sentence and then opened an
          inline confirm box beside it: *"I don't like how it looks … what I meant is making 'Turn
          this off' a link."* A banner is a sentence; the words that name the act are the place to
          press, and a second element repeating them is furniture. */}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger asChild>
          <button type="button" className="underline underline-offset-2 hover:no-underline">
            Turn this off
          </button>
        </DialogTrigger>
        <DialogContent>
          <DialogTitle>Turn off plain HTTP</DialogTitle>
          {/* ONE SENTENCE FOR EVERY READER, because before the write quince cannot tell them apart:
              this branched on `useInsecureOrigin()`, which is false for everybody who can see this
              banner, so the half that mattered never rendered.

              AND IT SAYS WHERE THE WAY BACK IS. Somebody about to close their own door should be
              told the door reopens from the inside — Settings → Sign-in, while they are still
              signed in. That sentence is only honest because `PlainHTTPSetting` exists; if it is
              ever removed, this copy is a lie and the act becomes a lockout again. */}
          <DialogDescription>
            quince will stop accepting sign-ins over plain HTTP. You stay signed in here, and anyone
            signing in at this address will need HTTPS from now on. You can allow it again in
            Settings → Sign-in.
          </DialogDescription>
          <div className="mt-4 flex items-center gap-2">
            <Button variant="destructive" onClick={() => void turnOff()} disabled={busy}>
              {busy ? "Turning it off…" : "Turn it off"}
            </Button>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
              Cancel
            </Button>
          </div>
          {failed ? (
            // THE SERVER'S OWN SENTENCE, as every other confirm in this codebase shows: a 409 here
            // means this browser holds no session, and "sign in to change this" is the useful fact.
            <p role="alert" className="mt-3 text-sm text-danger">
              {failed}
            </p>
          ) : null}
        </DialogContent>
      </Dialog>{" "}
      once you can reach quince over HTTPS.
    </div>
  );
}
