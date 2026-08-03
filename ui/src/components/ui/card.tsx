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
// So any `data-*` a caller relies on must be added here. That is the cost of an explicit list and
// also the point of it: a spread would have fixed this one instance and left the next one equally
// unfalsifiable.
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
