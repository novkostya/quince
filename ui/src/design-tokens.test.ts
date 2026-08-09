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

const PATTERN = new RegExp(`\\b(?:${PREFIXES.join("|")})-(?:${FOREIGN.join("|")})\\b`);

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
  it("uses no shadcn palette names that this project does not define", () => {
    const offenders: string[] = [];
    for (const file of sources(join(__dirname))) {
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (!line.includes("className")) return;
          const hit = PATTERN.exec(line);
          if (hit) offenders.push(`${file.replace(__dirname, "src")}:${i + 1}  ${hit[0]}`);
        });
    }
    expect(
      offenders,
      "these resolve to nothing, or — for `bg-muted` and friends — to a FOREGROUND colour used as a " +
        "surface. See ui/src/index.css for the palette: bg, card, elevated, line, fg, muted, subtle, " +
        "accent, accent-fg, accent-soft, ok, warn, danger.",
    ).toEqual([]);
  });

  // The gate is worthless if the pattern has drifted from the palette, so this asserts it still
  // fires — the same discipline as `privacy-check`'s canary.
  it("still matches a known-bad class (the canary)", () => {
    expect(PATTERN.test('className="text-muted-foreground"')).toBe(true);
    expect(PATTERN.test('className="border-border"')).toBe(true);
    expect(PATTERN.test('className="text-muted border-line bg-card"')).toBe(false);
  });
});
