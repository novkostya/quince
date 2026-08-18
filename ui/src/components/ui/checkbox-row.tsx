import type { InputHTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/cn";

// THE ONE CHECKBOX ROW, because the alignment was a class string in three files and a class string
// is free to drift (quince#1227).
//
// Operator-reported from a live walk: *"an implementer made a new feature and it made messed up
// checkboxes."* The ordering is the finding rather than the arithmetic — the notifications screen
// landed AFTER the fix that removed this exact idiom one directory away. Nobody undid anything.
// They wrote the old form fresh, because the corrected one was page-local and a bare
// `<input type="checkbox" className="mt-0.5">` was still the easiest thing to type.
//
// WHAT THE MAGIC NUMBER WAS. `mt-0.5` is a fixed 2px nudge tuned against the OLD `text-sm`
// (14px/20px): top-aligned by `items-start` plus 2px put a ~13px native box within a pixel of the
// line's optical centre — correct by luck. quince#1192 moved `text-sm` to 16px/24px, half of a 4px
// line-height change is 2px, and 2px was the whole nudge, so the control ended up floating above
// its own label. A number tuned against one scale is a defect waiting for the next scale change.
//
// SO THE BOX IS CENTRED IN THE LABEL'S FIRST LINE BOX, derived from the same tokens `text-sm`
// resolves to. Move the scale and this follows; there is nothing left to re-tune. `calc()` over a
// Tailwind step is deliberate: `h-6` is the same 1.5rem today and would need a test pinning both
// tokens to stay true — a second place to keep in step, whose failure mode is a red test rather
// than a correct screen. This form has no second place.
//
// IT OWNS THE GEOMETRY AND NOT THE TYPOGRAPHY, which is the one design call here and is narrower
// than quince#1227 proposed. The issue asked for "control + label + hint"; a `hint` prop would have
// to pick one size, and the three call sites do not agree — the notifications hints are `text-xs`
// and the setup screen's are the label's own size. Deciding that here would be a look change riding
// in on an alignment fix, so the label is `children` and each site keeps the words it had. What
// broke was the geometry, and the geometry is what this owns.
//
// A SINGLE-LINE LABEL IS A HINT-LESS ROW, which is why `ConfigEditor`'s `items-center` case folds in
// rather than needing a second component: the line box is exactly one line tall, so centring the box
// inside it and centring it against a one-line label are the same position.
//
// NESTED, NOT `htmlFor`. Radix associates implicitly this way and a checkbox's label belongs beside
// it rather than above it, so the association reason `Field` exists for is already satisfied.
//
// `type` IS NOT OVERRIDABLE, AND THE SPREAD ORDER IS WHAT ENFORCES IT. `{...input}` comes BEFORE
// the attribute, so a caller writing `<CheckboxRow type="radio">` — which type-checks, since
// `InputHTMLAttributes` carries `type` — gets a checkbox rather than a silent radio. The name of
// this component is a promise, and quince#1227's open question is whether anything hand-rolls the
// same alignment with a radio or a switch: if it does, this row is what somebody will reach for,
// and the reach must not quietly succeed.
//
// `className` LANDS ON THE LABEL, not on the input. That is right for the geometry — the label is
// what carries the flex row and the spacing a caller wants to adjust — and it is not derivable from
// the signature, which is why it is written here.
export function CheckboxRow({
  className,
  children,
  ...input
}: InputHTMLAttributes<HTMLInputElement> & { children: ReactNode }) {
  return (
    <label className={cn("flex items-start gap-2.5 text-sm", className)}>
      <span className="flex h-[calc(var(--type-sm)*var(--type-sm-line))] shrink-0 items-center">
        <input {...input} type="checkbox" />
      </span>
      {children}
    </label>
  );
}
