import { type ReactNode } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cn } from "@/lib/cn";

export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;

// NOTHING HERE READS THE VISUAL VIEWPORT, AND THAT IS THE POINT — Operator direction 2026-08-15.
//
// quince#762 built a JavaScript frame that tracked `visualViewport` so the dialog stayed centred in
// the visible area while the keyboard was up, and quince#816 is what that cost: the browser paints
// its own scroll before it reports it, so every correction lands one frame late and the dialog jumps
// on every focus change. That is not a bug in the compensation — it is the compensation working as
// fast as the platform permits, which is why tuning it was never going to end.
//
// Two things changed since, and together they remove the reason to compensate at all:
//
//  - quince#838 — THE DOCUMENT IS THE SCROLLER AGAIN. The shell no longer pins itself to the
//    viewport with an inner scroll region, so the browser owns scrolling and can do its own
//    scroll-into-view when the keyboard covers a field.
//  - quince#846 — THE HEAVY SURFACES ARE PAGES. Add-storage was the tall dialog that needed a
//    bounded, scrolling card measured against the keyboard; it is a route now.
//
// So the frame is plain CSS: the viewport, inset by the safe area, with the card centred in it and
// a scroll that only engages when the card is taller than the screen (see below). The safe-area
// padding is the ONE part of quince#762 that stays, because it is a fact about portalling rather
// than about keyboards — a Radix portal renders into <body>, so it inherits none of `AppLayout`'s
// insets and would sit under the Dynamic Island without this (see `index.css`).
//
// WHAT SAFARI DOES WITH THE KEYBOARD IS NOW SAFARI'S BUSINESS. iOS does not shrink the layout
// viewport for the keyboard; it pans the visual viewport over it, which moves fixed content on
// screen without moving it in layout. Left alone, that pan is what brings a focused field out from
// behind the keyboard. The old frame fought it and then chased it. This one does neither.
export function DialogContent({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <DialogPrimitive.Portal>
      {/* THE OVERLAY IS THE SCROLLER, AND THE CARD IS NOT — Operator-reported 2026-08-15, landscape,
          with the encryption dialog's head under the top edge and its buttons off the bottom.
          A dialog with no scroller anywhere is unreachable at BOTH ends the moment it outgrows the
          screen, and landscape makes that ordinary rather than rare: four fields do not fit in
          ~390px however short the dialog is in portrait.

          `min-h-full` on the inner flex row, NOT `h-full`, is the whole trick. When the card is
          shorter than the screen the row is exactly one screen tall and `items-center` centres it —
          identical to having no scroller at all, which is the state that was just confirmed good on
          a device. When the card is taller the row GROWS with it, so centring has nothing to
          overflow, and the overlay scrolls the row. There is no `max-height` anywhere and nothing
          measures anything.

          WHY NOT PUT THE SCROLL BACK ON THE CARD, which is the smaller change: a card bounded to the
          screen fills it edge to edge, so its own head and foot scroll away inside their own frame
          and the rounded corners sit against the safe area. Scrolling the overlay moves the card as
          an object — the shape the device just proved it likes — and keeps the gutter at both ends.

          Radix documents this exact nesting for tall content: `Content` INSIDE `Overlay` rather than
          beside it. `overscroll-contain` stops the scroll chaining to the page behind. */}
      <DialogPrimitive.Overlay
        className={cn(
          "fixed inset-0 z-50 overflow-y-auto overscroll-contain",
          "bg-black/40 backdrop-blur-sm",
        )}
      >
        {/* `max(1rem, …)` is the shell's own idiom (`AppLayout`, `PasswordForm`, the onboarding
            page): the inset when there is one, a plain margin when there is not, never both added.
            It rides INSIDE the scroll region, so a scrolled-to-the-top card still clears the notch
            and a scrolled-to-the-bottom one still clears the home indicator. */}
        <div
          className={cn(
            "flex min-h-full items-center justify-center px-4",
            "pt-[max(1rem,var(--safe-top))] pb-[max(1rem,var(--safe-bottom))]",
          )}
        >
          <DialogPrimitive.Content
            className={cn(
              "w-full max-w-md",
              "rounded-card border border-line bg-card p-6 shadow-xl focus:outline-none",
              className,
            )}
          >
            {children}
          </DialogPrimitive.Content>
        </div>
      </DialogPrimitive.Overlay>
    </DialogPrimitive.Portal>
  );
}

export function DialogTitle({ children }: { children: ReactNode }) {
  return (
    <DialogPrimitive.Title className="text-base font-semibold tracking-tight">
      {children}
    </DialogPrimitive.Title>
  );
}

export function DialogDescription({ children }: { children: ReactNode }) {
  return (
    <DialogPrimitive.Description className="mt-1 text-sm text-muted">
      {children}
    </DialogPrimitive.Description>
  );
}
