import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/cn";

export const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 rounded-lg text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        accent: "bg-accent text-accent-fg hover:opacity-90",
        outline: "border border-line bg-card text-fg hover:bg-elevated",
        ghost: "text-muted hover:bg-elevated hover:text-fg",
        destructive: "bg-danger text-white hover:opacity-90",
      },
      // Taller on phones for a comfortable touch target, current density on desktop (qn.6a mobile
      // pass): base = mobile, sm: = desktop.
      size: {
        sm: "h-9 px-3 sm:h-8",
        md: "h-10 px-4 sm:h-9",
        icon: "h-10 w-10 sm:h-9 sm:w-9",
      },
    },
    defaultVariants: { variant: "accent", size: "md" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export function Button({ className, variant, size, asChild = false, type, ...props }: ButtonProps) {
  const Comp = asChild ? Slot : "button";

  // `type="button"` BY DEFAULT, opt into `"submit"` explicitly (quince#828).
  //
  // HTML's default for a `<button>` inside a `<form>` is `submit`, so the dangerous behaviour was
  // the one you got by writing nothing. Every call site in the product happened to be correct, held
  // there entirely by each author remembering — and that has already been paid for twice:
  // quince#820 set `type="button"` on five buttons to wrap one dialog in a form, and quince#824 is
  // the second instance.
  //
  // A LINT RULE WAS THE ALTERNATIVE AND IT CANNOT SEE THIS TREE'S ACTUAL SHAPE. `AuthPage` renders
  // the `<form>` and its buttons arrive as `children` from `PasswordForm` and `SetupPasswordPage` —
  // different files. Whether a given `<Button>` sits inside a form depends on whether a caller three
  // components away passed `onSubmit`, which no per-file rule can decide. A default fixes the class
  // by construction where a rule would cover only the cases it can see.
  //
  // NOT DEFAULTED UNDER `asChild`: the rendered element is then whatever the caller passed — often
  // an `<a>` — where `type` means something else entirely, or nothing. An explicit `type` is still
  // forwarded, because that is the caller's decision about their own element.
  const typeProps = asChild ? (type === undefined ? {} : { type }) : { type: type ?? "button" };

  return (
    <Comp className={cn(buttonVariants({ variant, size }), className)} {...typeProps} {...props} />
  );
}
