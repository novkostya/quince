import type { FormEvent, ReactNode } from "react";

// The auth surfaces' shared LAYOUT PRIMITIVE — qn.6m slice 3, D1 and D2.
//
// D2 is explicit that the onboarding auth surface and the settings one are SIBLINGS rather than one
// component, because one has a session and one does not, and a single component switching on
// `status` would carry both sets of affordances in one tree behind a boolean. That argument is about
// AFFORDANCES. It is not about the box they sit in — D2 says in as many words that they "share the
// LAYOUT primitive and nothing else", and this is that primitive.
//
// So the `variant` below is deliberately the ONLY switch here, it decides nothing but CSS, and
// nothing in this file knows what an auth state is. If a condition ever appears in here that reads
// auth status, permissions or a capability, it is in the wrong file.
export type AuthVariant = "card" | "page";

export function AuthPage({
  variant = "card",
  title,
  subtitle,
  notice,
  onSubmit,
  children,
}: {
  // `card` is the DEFAULT because login keeps it — D1: ruling A is about the setup surface and the
  // settings surface. `/login` is a recurring destination on an existing install holding two fields
  // and a button, and a full-width page for that is emptier rather than calmer.
  variant?: AuthVariant;
  title: string;
  subtitle: string;
  notice?: ReactNode;
  // When present the box is a <form>, so Enter submits. Absent, it is a plain <div> — a surface with
  // several independent actions (the settings-side page, slice 6) has no single thing to submit, and
  // a form wrapping it would make every button a submit button by default. That defect has already
  // been paid for twice on this project (quince#824, quince#828).
  onSubmit?: (e: FormEvent) => void;
  children: ReactNode;
}) {
  const isPage = variant === "page";

  // `min-h-dvh` (not 100vh, and NOT `svh`), AND THE UNIT IS NOT THE THING TO CHANGE (quince#659).
  // An issue was filed saying the opposite — that `dvh` with the toolbars hidden is "larger than the
  // visible area" — and that is the definition of `lvh`. Per CSS Values 4 the dynamic viewport equals
  // the SMALL viewport when the toolbars are expanded and the LARGE one when they retract, so it
  // tracks what is visible in BOTH states:
  //
  //     svh   toolbars retracted: SHORTER than visible  |  expanded: equals visible
  //     lvh   toolbars retracted: equals visible        |  expanded: TALLER than visible
  //     dvh   toolbars retracted: equals visible        |  expanded: equals visible
  //
  // `min-h-svh` here would make the box shorter than the viewport whenever the toolbars retract —
  // importing a background band onto the login screen to fix a problem that does not exist.
  //
  // WHAT `dvh` CAN DO is lag transiently during the toolbar animation, and whether that lag is a
  // defect depends on the SCROLL STRUCTURE around it rather than on the unit. The document scrolls
  // here, so the worst a lag costs is a brief scroll that resolves itself.
  //
  // THAT USED TO BE A CONTRAST WITH THE AUTHED SHELL AND IT NO LONGER IS. The sentence here read:
  // "In the authed shell — `overflow-hidden`, no document scroll — the same lag puts content where
  // no scroller reaches it. That is quince#649." The shell stopped being a scroll container under
  // quince#838 (Operator direction: let Safari scroll the document natively), and that is what
  // closes quince#649 — rather than sizing around it. The reasoning above is unchanged and is now
  // simply true everywhere.
  //
  // THE UNIT STILL DIFFERS FROM THE SHELL'S, DELIBERATELY. This box is meant to FILL the visible
  // area, which is `dvh`'s job: it equals the visible height in both toolbar states. The shell's
  // `min-h-svh` answers a different question — a document MINIMUM that must not grow when the
  // toolbars collapse, because a growing minimum flips a just-taller-than-screen page between
  // scrollable and not. `docs/ui.design.md` carries both.
  //
  // Safe-area padding keeps content clear of the status bar and the side notch (qn.6a soak fixes).
  const outer = isPage
    ? // A PAGE TOP-ALIGNS. It is a destination with room to grow — slice 4 adds a passkey offer here
      // — and vertically centring a tall step makes its top edge move as the content changes.
      // Matches `OnboardingStoragePage`, which is the sibling step this one is being made to look
      // like, rather than `OnboardingHTTPSPage`, which is wider because it is prose to READ.
      "min-h-dvh bg-bg pb-16 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(2.5rem,env(safe-area-inset-top))] text-fg"
    : // A CARD sits toward the top on a phone so the keyboard / Face ID sheet has room below it
      // (dead-centring looks unbalanced once the sheet slides up), and centres on desktop.
      "flex min-h-dvh items-start justify-center bg-bg pb-6 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(4rem,env(safe-area-inset-top))] text-fg sm:items-center sm:py-6";

  const box = isPage
    ? "mx-auto w-full max-w-2xl"
    : "w-full max-w-sm rounded-card border border-line bg-card p-6";

  const inner = (
    <>
      <div className="text-lg font-semibold tracking-tight">quince</div>
      {/* A page's heading is `text-xl`, a card's is `text-base` — the same step `OnboardingStorage`
          takes. A card is a component on a screen; a page IS the screen, and a heading that does not
          grow with the box reads as a card someone forgot to draw a border around. */}
      <h1 className={(isPage ? "text-xl" : "text-base") + " mt-4 font-semibold tracking-tight"}>
        {title}
      </h1>
      <p className="mt-1 text-sm text-muted">{subtitle}</p>
      {notice}
      {children}
    </>
  );

  return (
    <div className={outer}>
      {onSubmit ? (
        <form onSubmit={onSubmit} className={box}>
          {inner}
        </form>
      ) : (
        <div className={box}>{inner}</div>
      )}
    </div>
  );
}
