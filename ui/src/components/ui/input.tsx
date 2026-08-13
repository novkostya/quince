import * as React from "react";
import { cn } from "@/lib/cn";
import { fieldBase } from "./field";

// Input is every text field in the product.
//
// IT DEFAULTS TO NO AUTOCAPITALISATION AND NO AUTOCORRECT, and that is a decision about what quince
// asks for rather than a stylistic default (Operator-reported from a phone, 2026-08-13).
//
// **iOS capitalises the first letter of a text field by default**, and the software keyboard opens
// shifted. Every free-text field quince has is a case-sensitive technical identifier — a path, a ZFS
// dataset, a hostname, a remote user, a socket address — so the platform default turns `rpool/quince`
// into `Rpool/quince`, `nas.local` into `Nas.local` and `quince` into `Quince`. Each of those is a
// value that fails somewhere else later: a dataset that does not exist, a host that does not resolve,
// a user with no `authorized_keys` entry. **The failure never names the capital letter.**
//
// Autocorrect is off for the same reason and is the sharper of the two on a phone: iOS will happily
// turn a hostname into a dictionary word.
//
// THE DEFAULT IS `none` RATHER THAN A PER-FIELD OPT-IN because the ratio decides it. Of this
// product's text inputs, exactly one takes human language — a passkey's nickname — and it opts back
// in explicitly. Every other one, present and future, is technical, so a default of `sentences`
// makes every new field wrong until somebody remembers.
//
// `{...props}` SPREADS LAST, so any caller can override all three. That is what makes the passkey
// field's opt-in work rather than needing a second component.
export function Input({ className, ...props }: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(fieldBase, "placeholder:text-subtle", className)}
      autoCapitalize="none"
      autoCorrect="off"
      spellCheck={false}
      {...props}
    />
  );
}
