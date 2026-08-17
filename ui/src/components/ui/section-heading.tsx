import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

// THE ONE SECTION HEADING, because there were two and neither was a level (quince#1155).
//
// Operator-reported from the deployed build: *"section headers have not been aligned: Passkeys is
// how it looks like in the rest of the app, but the rest of Sign-in settings page is different."*
// Exactly right, and the count was 13 to 7 — `text-sm font-semibold text-muted` on page sections
// (Devices, Storage, Space, Backups here, Passkeys …) against `text-sm font-semibold` on the
// settings cards (Change your password, Signing in over plain HTTP …). Structurally the two are the
// same thing: a `<section>`, an `<h2>`, and one explanatory `<p className="mt-1 text-sm text-muted">`
// under it. Same level, same shape, two colours, decided per file.
//
// THE HARDER HALF OF THAT REPORT IS THE ONE THIS SIZE ANSWERS: *"I don't like grey section headers,
// white reads better, but on the other hand section header looks exactly like subsection header."*
// Both halves were true and they were in tension only because the heading had nowhere to differ
// except colour — it was `text-sm`, which is what `Label` is, so a section heading and a field
// label were the same size and grey was doing all the work of the hierarchy.
//
// The survey is the reason this is the fix rather than a preference. quince's Settings page measured
// a type scale of exactly TWO steps, [14, 16], against a comparison set carrying four to six; a page
// with two steps has no room for `title > section > label > body` and must borrow colour to fake the
// third. `text-lg` buys the step back — 24px page title, **20px section**, 18px card title, 16px
// label and body — after which the heading can be `--fg` like everything else and the grey is not
// needed for anything. So the answer to "grey or white" is white, and it stops costing a level.
export function SectionHeading({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <h2 className={cn("text-lg font-semibold tracking-tight text-fg", className)}>{children}</h2>
  );
}
