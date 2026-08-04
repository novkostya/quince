import * as React from "react";
import { cn } from "@/lib/cn";
import { fieldBase } from "./field";

// The full-width form `<select>` — `Input`'s shape, and now literally `Input`'s class string
// (quince#616). Native rather than Radix, matching `input.tsx`: this replaces a native `<select>`
// and swapping the primitive would be a redesign, not the fix.
//
// NOT for the small inline selects in `StorageSelect` / `BackupControls`. Those read `to <select>`
// and `over <select>` inside a 12px label — a different control at a different size, and dropping
// a 16px full-width field into a 12px sentence is the wrong outcome. Ruled on quince#616.
export function Select({ className, ...props }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className={cn(fieldBase, className)} {...props} />;
}
