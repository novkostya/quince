import { BackLink } from "@/components/BackLink";

import { ChevronLeft } from "lucide-react";
import { Passkeys, usePasskeyList } from "@/features/settings/Passkeys";
import { PasswordControls, RemovePasswordSection } from "@/features/settings/PasswordControls";
import { PlainHTTPSetting } from "@/features/settings/PlainHTTPSetting";
import { credentialState } from "@/features/settings/credentialState";

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
  // THE PAGE READS THE CREDENTIAL STATE BECAUSE THE ORDER FOLLOWS IT — quince#1316, Operator
  // 2026-08-19: *"In passwordless setup Passkeys section must be the first — going passwordless is
  // your choice and we should respect that."* The principle is **lead with the credential the user
  // actually signs in with**, so the order is a fact about the install rather than a constant.
  //
  // THE THIRD CONSUMER OF ONE QUERY, not a second request. `Passkeys` owns the fetch and
  // `PasswordControls` already shared it; this is the same key, served from the same cache.
  const list = usePasskeyList();
  const hasPassword = list.data?.has_password ?? true;
  const credentials = credentialState(list.data, hasPassword);

  // WHICH SIDE LEADS. `has-password` leads with the password because that is what signs you in;
  // `unconfigured` leads with the panel that says nothing can, because that is the more urgent
  // fact and the remedy is a password. The two passkey states lead with `Passkeys`.
  //
  // THE LOADING FALLBACK IS `has-password`, THE SAME GUESS `PasswordControls` MAKES. Deliberately
  // identical: two fallbacks that disagree would order the page against the words rendered inside
  // it for one frame, which reads as a flicker with no cause.
  const passwordLeads = credentials === "has-password" || credentials === "unconfigured";

  return (
    <section>
      {/* A WAY BACK, because this is the first settings page that is not itself `/settings`. The
          sidebar highlights Settings but does not return you to it, and a phone user's alternative
          is the browser's back gesture — which works, and which nothing on screen promises. */}
      <BackLink
        to="/settings"
        className="-ml-1 inline-flex items-center gap-1 text-sm text-muted transition-colors hover:text-fg"
      >
        <ChevronLeft size={16} strokeWidth={1.75} />
        Settings
      </BackLink>
      <h1 className="mt-2 text-xl font-semibold tracking-tight">Sign-in</h1>
      {/* NAMED FOR WHAT THE USER DOES, not for the subsystem. "Authentication" is the word the code
          uses; "Sign-in" is the word the person reading this screen used ten seconds ago. The route
          keeps `/settings/auth` because a URL is addressed by developers and by nobody else. */}
      <p className="mt-1 text-sm text-muted">
        How you get into quince — your password, and the passkeys registered to this address.
      </p>

      <div className="mt-6 max-w-xl">
        {/* THE ORDER IS CONDITIONAL, AND THE COMMENT THAT STOOD HERE SAID IT WAS FIXED. It read
            "PASSKEYS FIRST, PASSWORD SECOND — qn.6m slice 6b", which was a decision taken when
            every install got one order. The rung's reason survives the ruling unchanged: typing an
            admin password on a phone is the worst part of using quince, so a passwordless install
            leads with the thing that replaced it — and an install that still signs in with a
            password leads with that instead, rather than being shown someone else's setup first. */}
        {passwordLeads ? <PasswordControls /> : null}
        <Passkeys />
        {passwordLeads ? null : <PasswordControls />}
        {/* THE DESTRUCTIVE ACTION STAYS FURTHEST FROM THE TOP — the property the old comment
            claimed, now the reason this section is mounted HERE rather than inside
            `PasswordControls`. Removing the password requires a passkey to confirm it, so the
            offer has to sit below the list of passkeys that can: the ruled order is password →
            passkeys → remove, and a component cannot place a sibling it does not render. It
            guards itself and renders nothing in the three states that do not offer it. */}
        <RemovePasswordSection />
        {/* TRANSPORT LAST, because it is the least often changed and the most dangerous to change
            by accident — and because it is the reversal path for the banner's own control
            (quince#1069), so it belongs where somebody signed in can find it rather than where
            somebody is choosing a password. */}
        <PlainHTTPSetting />
      </div>
    </section>
  );
}
