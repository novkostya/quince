import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

export function Card({
  className,
  children,
  "data-testid": testId,
}: {
  className?: string;
  children: ReactNode;
  "data-testid"?: string;
}) {
  return (
    <div className={cn("rounded-card border border-line bg-card", className)} data-testid={testId}>
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
