import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// THE VENDORED-shadcn VOCABULARY IS NOT THIS PROJECT'S, AND TAILWIND WILL NOT TELL YOU.
//
// `ui/src/index.css` defines the palette: `bg`, `card`, `elevated`, `line`, `fg`, `muted`, `subtle`,
// `accent`, `accent-fg`, `accent-soft`, `ok`, `warn`, `danger`. The components here are shadcn-STYLE
// but the tokens were renamed, so shadcn's own names — `muted-foreground`, `border`, `background`,
// `primary`, `destructive` — resolve to nothing.
//
// **Nothing catches that.** Tailwind emits no rule for an unknown utility, `tsc` sees a string, and
// ESLint has no opinion. Both `pnpm lint` and `pnpm build` pass, and the defect is visible only on a
// screen. It reached the Operator's phone once (the `qn.6i` reconciling notice) and was in a second
// place in the same rung.
//
// **THE WORST NAME IS THE ONE THAT EXISTS.** `--color-muted` maps to `--fg-muted`, a FOREGROUND
// colour — so `bg-muted` is a valid utility that fills a surface with the muted TEXT colour. It does
// not disappear, it renders a pale slab. The two that resolve to nothing fail invisibly; this one
// fails loudly and wrongly, which is why a denylist is worth more here than a "does it exist" check.
//
// A DENYLIST RATHER THAN AN ALLOWLIST, deliberately. An allowlist over every `text-*` / `border-*`
// utility has to know that `text-sm`, `border-b`, `bg-none` and every future Tailwind built-in are
// not colours — it would be a Tailwind reimplementation with a false-positive for every version bump.
// This list is closed: it is shadcn's palette, it does not grow, and every entry is a name this
// project has deliberately declined to define.
const FOREIGN = [
  "background",
  "foreground",
  "card-foreground",
  "popover",
  "popover-foreground",
  "primary",
  "primary-foreground",
  "secondary",
  "secondary-foreground",
  "muted-foreground",
  "accent-foreground",
  "destructive",
  "destructive-foreground",
  "border",
  "input",
  "ring",
];

const PREFIXES = ["bg", "text", "border", "ring", "fill", "stroke", "divide", "outline", "shadow"];

// RULE 1 — a name from another palette. Resolves to nothing; fails invisibly.
const FOREIGN_PATTERN = new RegExp(`\\b(?:${PREFIXES.join("|")})-(?:${FOREIGN.join("|")})\\b`, "g");

// RULE 2 — A LOCAL TOKEN ON THE WRONG KIND OF SURFACE, which is the case that actually shipped and
// which rule 1 structurally CANNOT catch (quince#778 review).
//
// The denylist above holds `muted-foreground`, and it can never hold `muted`, because `text-muted`
// is correct and used everywhere. So `bg-muted` — the class that rendered a pale slab on a real
// screen — sailed through the guard written because of it. The first version of this file said in
// its own assertion message that `bg-muted` was covered. It was not.
//
// The distinction is not foreign-versus-local, it is **which surface a token may be used on**:
// `fg`, `muted`, `subtle` and `accent-fg` are FOREGROUND colours, so a `bg-`/`border-`/`divide-`
// prefix is wrong even though Tailwind resolves it happily.
const FG_ONLY = ["fg", "muted", "subtle", "accent-fg"];
const SURFACE = ["bg", "border", "divide", "ring", "outline"];
const MISPLACED_PATTERN = new RegExp(
  `\\b(?:${SURFACE.join("|")})-(?:${FG_ONLY.join("|")})\\b`,
  "g",
);

// `g` ON BOTH, because a first-match-per-line regex makes fixing iterative: the line that shipped
// carried TWO violations and the first version reported one, so a reader fixes it, re-runs, and is
// told about the next one (quince#778 review).
function violations(line: string): string[] {
  return [...line.matchAll(FOREIGN_PATTERN), ...line.matchAll(MISPLACED_PATTERN)].map((m) => m[0]);
}

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [p] : [];
  });
}

describe("design tokens", () => {
  // SCOPED TO LINES CARRYING `className`, and the limitation is stated rather than hidden: a class
  // string wrapped onto its own line, away from the attribute, would be missed. That is not the
  // shape prettier produces here, and the alternative — scanning every line — flags this file and
  // any comment that names the tokens it forbids, which is how a gate becomes something people
  // silence.
  it("uses no token that resolves to nothing, and no foreground token as a surface", () => {
    const offenders: string[] = [];
    for (const file of sources(join(__dirname))) {
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (!line.includes("className")) return;
          for (const hit of violations(line)) {
            offenders.push(`${file.replace(__dirname, "src")}:${i + 1}  ${hit}`);
          }
        });
    }
    expect(
      offenders,
      "either the name is from another palette and Tailwind emitted NOTHING for it, or it is one of " +
        "ours used on the wrong surface — `fg`, `muted`, `subtle` and `accent-fg` are FOREGROUND " +
        "colours, so `bg-muted` fills a box with the muted TEXT colour rather than failing. The " +
        "palette is in ui/src/index.css: bg, card, elevated, line, fg, muted, subtle, accent, " +
        "accent-fg, accent-soft, ok, warn, danger.",
    ).toEqual([]);
  });

  // THE CANARY, and it is what the first version of this file most needed. A guard is worthless if
  // its pattern has drifted from the palette — and worse than worthless if it reports coverage it
  // does not have, which is exactly what shipped: `bg-muted` was named in the assertion message and
  // matched by nothing.
  it("catches every class that shipped the defect, and clears the correct ones", () => {
    // All three from the reported line, each on its own — `bg-muted` is the one rule 1 cannot see.
    expect(violations('className="bg-muted"')).toEqual(["bg-muted"]);
    expect(violations('className="text-muted-foreground"')).toEqual(["text-muted-foreground"]);
    expect(violations('className="border-border"')).toEqual(["border-border"]);

    // BOTH violations on one line, which a non-global regex reported as one.
    expect(violations('className="border-border bg-muted"')).toHaveLength(2);

    // The house surface, and the correct use of a foreground token, stay clean.
    expect(violations('className="rounded-card border border-line bg-card text-muted"')).toEqual([]);
    expect(violations('className="text-fg text-subtle text-accent-fg"')).toEqual([]);
  });
});
