import { useInsecureTransportAllowed } from "@/lib/health";

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
  // RENDERS NOTHING WHEN OFF, which is the shipping default and almost every install. A permanent
  // element that merely changed wording would train people to stop reading it.
  if (!useInsecureTransportAllowed()) return null;
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
      {/* WRITTEN FOR THE PERSON READING IT — Operator direction, 2026-08-16 (quince#1069), on the
          copy this replaces:

            "Your sign-in cookie and CSRF token cross the network unencrypted … Turn off
             sessions.allow_insecure_transport once you have HTTPS working."

          A config key is not a sentence, and a CSRF token is a detail nobody outside this codebase
          can act on. Both were written for a reader who already knows the system — which is every
          reader this text will never have.

          IT STILL NAMES WHAT IS UNPROTECTED, which is quince#446's ruling. "Your sign-in travels in
          the clear" is that claim in words a person can weigh; the cookie and the token are HOW, and
          how is not what somebody about to type a password needs to decide anything.

          IT IS ABOUT THE INSTALL, NOT THIS CONNECTION, and that survives the rewrite deliberately.
          The setting is global, so a reader can be on a perfectly good HTTPS address while it is on
          — "this connection is not encrypted" would be a lie to exactly that reader. */}
      <strong className="text-danger">quince is allowing plain HTTP.</strong> Your sign-in travels in
      the clear, so anyone who can see the traffic can sign in as you. Turn this off once you can
      reach quince over HTTPS.
    </div>
  );
}
