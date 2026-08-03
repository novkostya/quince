import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

// `data-*` attributes are forwarded EXPLICITLY, one by one, and the list is not an oversight
// waiting to be turned into a spread.
//
// TypeScript does not check hyphenated JSX attributes against a component's props type, so
// `<Card data-storage-name={...}>` typechecks whether or not Card does anything with it — and
// until qn.6d's e2e work, Card did not. `StorageCard` had carried that exact attribute since it
// was written: present in the source, absent from the DOM, passing `gates-ui` throughout. It was
// found by a Playwright selector that could not match it, which is the only thing that could have
// found it.
//
// So any `data-*` a caller relies on must be added here. WHAT THAT BUYS IS A NARROW, AUDITABLE
// SURFACE, not safety: a spread (`{...props}`) would forward whatever a caller passed — handlers,
// `style`, `aria-*`, anything — onto a vendored primitive that deliberately controls its own
// contract, and the set of attributes a `Card` supports would stop being reviewable. Adding one
// should cost a deliberate edit here, and that is the whole argument for the list.
//
// WHAT IT DOES NOT BUY, stated because the obvious claim is false: it does not make a mistake
// detectable. `<Card data-storage-nme={x}>` typechecks and silently does nothing whether this
// component spreads or lists — the props type never sees a hyphenated key either way. So on
// FORWARDING RELIABILITY a spread is strictly better, and this list is the deliberate trade.
//
// An earlier version of this comment claimed the reverse, and the correction is kept because it
// is load-bearing: a guard resting on a reason that does not hold is worse than no guard, since
// the next reader checks it, finds the reason false, converts to a spread, and never learns the
// constraint that was real (quince#598 review; the guard rule is quince#595).
export function Card({
  className,
  children,
  "data-testid": testId,
  "data-storage-name": storageName,
}: {
  className?: string;
  children: ReactNode;
  "data-testid"?: string;
  "data-storage-name"?: string;
}) {
  return (
    <div
      className={cn("rounded-card border border-line bg-card", className)}
      data-testid={testId}
      data-storage-name={storageName}
    >
      {children}
    </div>
  );
}

export function CardHeader({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("flex flex-col gap-1 p-5", className)}>{children}</div>;
}

export function CardTitle({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("text-sm font-semibold tracking-tight", className)}>{children}</div>;
}

export function CardContent({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("p-5 pt-0", className)}>{children}</div>;
}

export function CardFooter({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("flex items-center gap-2 p-5 pt-0", className)}>{children}</div>;
}
