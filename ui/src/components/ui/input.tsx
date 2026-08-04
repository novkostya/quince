import * as React from "react";
import { cn } from "@/lib/cn";
import { fieldBase } from "./field";

export function Input({ className, ...props }: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn(fieldBase, "placeholder:text-subtle", className)} {...props} />;
}
