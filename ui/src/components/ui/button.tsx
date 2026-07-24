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

export function Button({ className, variant, size, asChild = false, ...props }: ButtonProps) {
  const Comp = asChild ? Slot : "button";
  return <Comp className={cn(buttonVariants({ variant, size }), className)} {...props} />;
}
