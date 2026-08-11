import { Link } from "react-router-dom";
import { ChevronLeft } from "lucide-react";
import { Passkeys } from "@/features/settings/Passkeys";

// The auth surface as A PAGE OF ITS OWN — quince#841 ruling A, qn.6m D2.
//
// NOT A FOURTH BLOCK INSIDE SETTINGS, and the ruling gives the reason: **auth is not
// configuration**. Settings is a config editor, a config dump and storages; a fourth section there
// makes it a drawer. That argument was already half-applied before this page existed — quince#834
// mounts the passkeys card OUTSIDE the config block for exactly this reason — and this finishes it.
//
// SIBLING OF THE ONBOARDING AUTH SURFACE, NOT THE SAME COMPONENT (D2). One has a session and one
// does not: the onboarding page runs pre-auth against `POST /api/auth/setup` and can offer neither
// removal nor a credential list, while this one runs authenticated and offers both. A single
// component switching on `status` would carry both sets of affordances in one tree behind a boolean,
// which is how a surface that must refuse an unauthenticated caller ends up one inverted condition
// from not refusing. They share `AuthPage`'s LAYOUT and nothing else.
//
// INSIDE THE AUTHED SHELL, so it does NOT use `AuthPage`: that primitive owns a full-viewport
// wrapper with its own safe-area padding, and this route renders inside `AppLayout`'s `<main>`,
// which already provides both. Using it here would nest one viewport box inside another — the
// scroll-structure hazard quince#649 is about. The onboarding sibling is a top-level route and does
// use it; that asymmetry is the point of D2 rather than an inconsistency.
export function SettingsAuthPage() {
  return (
    <section>
      {/* A WAY BACK, because this is the first settings page that is not itself `/settings`. The
          sidebar highlights Settings but does not return you to it, and a phone user's alternative
          is the browser's back gesture — which works, and which nothing on screen promises. */}
      <Link
        to="/settings"
        className="-ml-1 inline-flex items-center gap-1 text-sm text-muted transition-colors hover:text-fg"
      >
        <ChevronLeft size={16} strokeWidth={1.75} />
        Settings
      </Link>
      <h1 className="mt-2 text-xl font-semibold tracking-tight">Sign-in</h1>
      {/* NAMED FOR WHAT THE USER DOES, not for the subsystem. "Authentication" is the word the code
          uses; "Sign-in" is the word the person reading this screen used ten seconds ago. The route
          keeps `/settings/auth` because a URL is addressed by developers and by nobody else. */}
      <p className="mt-1 text-sm text-muted">
        How you get into quince — your password, and the passkeys registered to this address.
      </p>

      <div className="mt-6 max-w-xl">
        <Passkeys />
      </div>
    </section>
  );
}
