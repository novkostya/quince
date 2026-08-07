import type { ReactNode } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cn } from "@/lib/cn";

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
      {/* A DIALOG TALLER THAN THE VIEWPORT WAS UNREACHABLE, NOT MERELY UGLY — Operator-reported on a
          phone, qn.6e. Centred with `-translate-y-1/2` and no height bound, tall content ran off
          BOTH edges at once and nothing scrolled: the buttons at the foot could not be reached and
          the dialog could only be dismissed.

          `max-h` + `overflow-y-auto` is the whole fix, and it belongs HERE rather than on the one
          dialog that grew: every dialog in the product had it, and the add-storage form is only the
          first that got tall enough to hit it. `overscroll-contain` stops the scroll chaining to the
          page behind the overlay once the dialog reaches its end.

          `dvh`, NOT `vh` — ruled on quince#659 and applied the same way in `PasswordForm` and the
          onboarding page. On a phone `vh` is the height with the toolbars HIDDEN, so a `vh`-bounded
          dialog is taller than what you can actually see whenever they are showing, which is the
          state a user is in when they open one. `dvh` tracks the visible area in both states.

          `2rem` matches the `w-[calc(100%-2rem)]` inset above, so the dialog is bounded by the same
          margin on all four sides rather than being flush to the top and bottom edges. */}
      <DialogPrimitive.Content
        className={cn(
          "fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2",
          "max-h-[calc(100dvh-2rem)] overflow-y-auto overscroll-contain",
          "rounded-card border border-line bg-card p-6 shadow-xl focus:outline-none",
          className,
        )}
      >
        {children}
      </DialogPrimitive.Content>
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
