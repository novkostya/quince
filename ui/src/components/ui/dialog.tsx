import { useRef, type ReactNode } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cn } from "@/lib/cn";
import { useVisualViewport } from "@/lib/useVisualViewport";
import { useScrollFocusIntoView } from "@/lib/useScrollFocusIntoView";

export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;

export function DialogContent({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/40 backdrop-blur-sm" />
      <DialogSurface className={className}>{children}</DialogSurface>
    </DialogPrimitive.Portal>
  );
}

// A SEPARATE COMPONENT SO THE HOOKS' LIFETIME IS THE DIALOG'S VISIBILITY, NOT ITS MOUNT.
// `DialogContent` is rendered by its consumer whether the dialog is open or shut — Radix decides
// visibility inside `Portal`, which renders nothing while closed. Hooks called up there would
// subscribe to viewport events for every dialog in the tree, permanently, and — worse for the scroll
// one — would run their effect against a `ref` that is still `null`, then never run again when the
// dialog actually opened. Rendered INSIDE the portal, these mount only while the dialog is on screen.
function DialogSurface({ className, children }: { className?: string; children: ReactNode }) {
  // Publishes `--vv-top` / `--vv-height` while this dialog is up. Without it the frame falls back to
  // `100dvh`, which is the visible height in every state except an open keyboard — see the hook for
  // why that one case cannot be done in CSS.
  useVisualViewport();

  // Keeps the focused field off the edges of the scroll region, including after the keyboard has
  // shrunk it. The card below is the scroll container, so it is the element that must be measured.
  const card = useRef<HTMLDivElement>(null);
  useScrollFocusIntoView(card);

  return (
    <>
      {/* THE FRAME IS THE VISIBLE AREA, AND THE DIALOG IS CENTRED INSIDE IT — quince#762.
          Operator-reported from a phone: every dialog in the product sat against the top of the
          screen with its head under the Dynamic Island.

          A dialog is portalled into <body>, so it is not a descendant of `AppLayout` and inherits
          none of the `env(safe-area-inset-*)` padding that keeps every other surface clear of the
          notch and the home indicator. Positioning it directly against the layout viewport is
          therefore wrong twice over: the layout viewport runs UNDER the notch (`viewport-fit=cover`
          in `index.html`), and on iOS it does not shrink for the keyboard either.

          So this element is a frame rather than a backdrop: it spans the VISIBLE area — offset and
          height from `--vv-*`, inset by the safe area on top and bottom — and flex-centres the card
          within it. Centring against a box that is already correct is what makes the result correct
          in all three states at once: notch, home indicator, keyboard.

          DO NOT COLLAPSE THIS BACK INTO `top-1/2 -translate-y-1/2` ON THE CONTENT. It reads as the
          same thing and is not: that centres against the layout viewport, which is the bug this
          replaced, and the keyboard half of it was Operator-reported twice before it was understood.

          `pointer-events-none` so a tap beside the card still reaches the overlay and dismisses;
          the card takes them back. The frame is a sibling of the overlay at the same `z-50`, drawn
          after it, so it sits above without needing a higher layer. */}
      <div
        className={cn(
          "pointer-events-none fixed inset-x-0 z-50 flex items-center justify-center px-4",
          "top-[var(--vv-top)] h-[var(--vv-height)]",
          // `max(1rem, …)` is the shell's own idiom (`AppLayout`, `PasswordForm`, the onboarding
          // page): the inset when there is one, a plain margin when there is not, never both added.
          "pt-[max(1rem,var(--safe-top))] pb-[max(1rem,var(--safe-bottom))]",
        )}
      >
        {/* `max-h-full` is the frame's CONTENT box — the visible height less the safe area — so the
            card can never exceed what is on screen and the frame's `items-center` can never push its
            head off the top. Taller content scrolls INSIDE the card instead of running off both
            edges, which is how a dialog became unreachable rather than merely clipped (qn.6e): the
            buttons at the foot could not be got to at all. `overscroll-contain` stops that scroll
            chaining to the page behind the overlay once the card reaches its end, and `p-6` rides
            inside the scroll region, so there is still a margin below the last control when you
            reach the bottom. */}
        <DialogPrimitive.Content
          ref={card}
          className={cn(
            "pointer-events-auto max-h-full w-full max-w-md overflow-y-auto overscroll-contain",
            "rounded-card border border-line bg-card p-6 shadow-xl focus:outline-none",
            className,
          )}
        >
          {children}
        </DialogPrimitive.Content>
      </div>
    </>
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
