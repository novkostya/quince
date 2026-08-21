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

// THE TRAILING BOUNDARY IS `(?![\w-])`, NOT `\b`, AND ALL THREE RULES DEPEND ON IT (quince#1436).
//
// `-` is a non-word character, so `\b` holds in the MIDDLE of a hyphenated token: against
// `text-primary-foreground` the alternation matches `primary`, `\b` succeeds because the next
// character is `-`, and the gate reports `text-primary` — a class that is not in the file. Measured
// before the fix, and it affects three of the shadcn names on its own list:
//
//   text-primary-foreground      -> ["text-primary"]
//   text-destructive-foreground  -> ["text-destructive"]
//   text-popover-foreground      -> ["text-popover"]
//
// The gate still FAILED, so nothing shipped because of it — but it named the wrong class, which is
// the troubleshooting rule's own case: a diagnostic sends the reader somewhere, and this one sent
// them looking for a class that does not exist.
//
// IT ALSO MATTERS ACROSS RULES, which is how it surfaced. `bg-fg-muted` is rule 3's, and rule 2
// matched `bg-fg` inside it first — two reports for one class, one of them fictional. A lookahead
// that refuses a following `-` or word character makes every rule stop at the whole token, so the
// longest correct name wins wherever two rules overlap.
const TOKEN_END = "(?![\\w-])";

// RULE 1 — a name from another palette. Resolves to nothing; fails invisibly.
const FOREIGN_PATTERN = new RegExp(
  `\\b(?:${PREFIXES.join("|")})-(?:${FOREIGN.join("|")})${TOKEN_END}`,
  "g",
);

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
  `\\b(?:${SURFACE.join("|")})-(?:${FG_ONLY.join("|")})${TOKEN_END}`,
  "g",
);

// RULE 3 — A COLOUR UTILITY THAT NAMES THE CSS VARIABLE INSTEAD OF THE TAILWIND ROLE. It emits
// NOTHING, exactly like rule 1, and rule 1 structurally CANNOT see it: `fg-muted` is not another
// palette's name, it is OUR OWN variable one layer too deep (quince#1436).
//
// `index.css` maps `--color-muted: var(--fg-muted)`, so `muted` is the role and `fg-muted` is the
// raw variable behind it. `text-muted` emits a rule; `text-fg-muted` emits nothing, so the text
// silently inherits — no error, no warning, and nothing on screen that looks wrong enough to chase.
//
// THE BLIND SPOT WAS IN THE TOKEN, NOT THE PREFIX, which is worth stating because the report
// reasonably guessed otherwise. quince#1436 measured `border-border` caught and `text-fg-muted`
// missed and asked whether `bg-*` / `ring-*` / `fill-*` shared it. They did not: rule 1 already
// spans every prefix. What no rule covered was a NAME that is neither foreign nor a role.
//
// DERIVED FROM index.css, NOT TYPED OUT, and that is the point rather than economy. This file's own
// canary exists because a guard whose pattern has drifted from the palette is worse than none — and
// a hand-written list of variable names is a second copy of the palette with nothing holding the two
// together. Reading the mapping means a renamed variable updates the rule in the same edit.
//
// THE PRICE HAS ALREADY BEEN PAID ONCE, LOCALLY. `NotificationsInstallPage` carried `text-fg-muted`
// in EIGHT places — every body-text block it had — and the remedy taken then was an assertion scoped
// to that one page's rendered output. Four days later the identical class landed in `EnrolPage` and
// nothing fired. A page-scoped guard against a project-wide shape catches the page that already had
// the bug.
const THEME = readFileSync(join(__dirname, "index.css"), "utf8");
const MAPPINGS = [...THEME.matchAll(/--color-([\w-]+):\s*var\(--([\w-]+)\)/g)];
const ROLES = new Set(MAPPINGS.map((m) => m[1]));

// A raw variable is dead as a utility token whenever it is not ALSO a role. `border` is excluded
// because rule 1 already owns it as shadcn's name: two rules matching one class would report it
// twice, and the canary below pins `border-border` at exactly one hit.
//
// LONGEST FIRST, AND THIS IS NOT COSMETIC. The pattern ends in `\b`, and `-` is a word boundary, so
// against `text-fg-muted` an alternation that offered `fg` before `fg-muted` would match `text-fg`
// and report the wrong class. `fg` is a role today and therefore excluded, so nothing exercises it —
// which is exactly why it is pinned here rather than left to a future palette to discover.
const DEAD = MAPPINGS.map((m) => m[2])
  .filter((raw) => !ROLES.has(raw) && !FOREIGN.includes(raw))
  .sort((a, b) => b.length - a.length);
const DEAD_PATTERN = new RegExp(
  `\\b(?:${PREFIXES.join("|")})-(?:${DEAD.join("|")})${TOKEN_END}`,
  "g",
);

// `g` ON ALL THREE, because a first-match-per-line regex makes fixing iterative: the line that
// shipped carried TWO violations and the first version reported one, so a reader fixes it, re-runs,
// and is told about the next one (quince#778 review).
function violations(line: string): string[] {
  return [
    ...line.matchAll(FOREIGN_PATTERN),
    ...line.matchAll(MISPLACED_PATTERN),
    ...line.matchAll(DEAD_PATTERN),
  ].map((m) => m[0]);
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
      "one of three things: the name is from another palette and Tailwind emitted NOTHING for it; " +
        "or it is one of ours used on the wrong surface — `fg`, `muted`, `subtle` and `accent-fg` " +
        "are FOREGROUND colours, so `bg-muted` fills a box with the muted TEXT colour rather than " +
        "failing; or it names the CSS VARIABLE instead of the role, like `text-fg-muted` for " +
        "`text-muted`, which also emits nothing. The palette is in ui/src/index.css: bg, card, " +
        "elevated, line, fg, muted, subtle, placeholder, accent, accent-fg, accent-soft, ok, warn, " +
        "danger — those are the ROLES, and the `--fg-*` / `--bg-*` variables behind them are not.",
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

  // RULE 3's OWN CANARY (quince#1436). `text-fg-muted` reached `EnrolPage` with a green ladder, and
  // the reason it is worth its own block is that the class it must catch is INVISIBLE: it emits no
  // rule, so nothing on screen looks broken enough to chase.
  it("catches a class that names the CSS variable instead of the role", () => {
    // Measured against the built stylesheet before this rule existed: `text-muted` emits 1 rule,
    // `text-fg-muted` emits 0, and the gate reported nothing.
    expect(violations('className="text-fg-muted"')).toEqual(["text-fg-muted"]);
    expect(violations('className="text-fg-subtle"')).toEqual(["text-fg-subtle"]);

    // NOT PREFIX-SPECIFIC, which is the question quince#1436 left open. Rule 1 already spanned every
    // prefix; what was missing was the NAME, so every prefix inherits the fix at once.
    expect(violations('className="bg-fg-muted"')).toEqual(["bg-fg-muted"]);
    expect(violations('className="ring-fg-muted"')).toEqual(["ring-fg-muted"]);
    expect(violations('className="fill-fg-muted"')).toEqual(["fill-fg-muted"]);

    // The ROLE spellings these are mistakes FOR must stay clean, or the rule is unusable.
    expect(violations('className="text-muted text-subtle text-placeholder"')).toEqual([]);

    // EXACTLY ONE HIT FOR `border-border`, pinned above and re-pinned here for a different reason:
    // `border` is a raw variable AND a foreign name, so without the FOREIGN exclusion in `DEAD` two
    // rules would match it and the report would double every occurrence.
    expect(violations('className="border-border"')).toHaveLength(1);
  });


  // THE BOUNDARY, and it is a REGRESSION canary rather than a new rule (quince#1436). Each of these
  // was reported as the truncated class before `TOKEN_END` replaced the trailing `\b`. The gate
  // failed either way; what was wrong was the name it printed.
  it("names the whole token, not a prefix of it", () => {
    expect(violations('className="text-primary-foreground"')).toEqual(["text-primary-foreground"]);
    expect(violations('className="text-destructive-foreground"')).toEqual([
      "text-destructive-foreground",
    ]);
    expect(violations('className="text-popover-foreground"')).toEqual(["text-popover-foreground"]);

    // ACROSS rules, which is how this surfaced: rule 2 saw `bg-fg` inside rule 3's `bg-fg-muted`,
    // so one class produced two reports and one of them named nothing in the file.
    expect(violations('className="bg-fg-muted"')).toEqual(["bg-fg-muted"]);

    // The short spellings these truncate TO are real violations in their own right and must still
    // be caught — the lookahead must not have turned the rules off.
    expect(violations('className="bg-fg"')).toEqual(["bg-fg"]);
    expect(violations('className="text-primary"')).toEqual(["text-primary"]);

    // A hyphenated name that is CORRECT stays clean, which is the other direction.
    expect(violations('className="bg-accent-soft text-accent-fg"')).toEqual([]);
  });

  // THE DERIVATION IS A CONTROL, NOT A CONVENIENCE — a parse that silently matched nothing would
  // leave rule 3 vacuous and every one of the assertions above passing for the wrong reason. This is
  // the one assertion that fails when index.css moves, is renamed, or changes its mapping syntax.
  it("derives the dead-token list from the stylesheet, and it is not empty", () => {
    expect(MAPPINGS.length).toBeGreaterThan(10);
    expect([...ROLES]).toContain("muted");
    expect(DEAD).toContain("fg-muted");
    expect(DEAD).toContain("fg-subtle");
    // A role is never dead: `muted` is what the correct class says.
    expect(DEAD).not.toContain("muted");
    // Rule 1 owns `border`; see the exclusion in DEAD.
    expect(DEAD).not.toContain("border");
  });
});
