import type { DomainSummary } from "@/lib/types";

// D3's partition: which DOMAINS belong to which APP, and what is left over.
//
// THE SPEC RULES THE LABEL AND LEAVES THE MAPPING OPEN. D3 decides that "apps" means the 21
// user-installed bundles from Info.plist, and that "the other 1,243 domains are not dropped —
// they are aggregated into a named row, so the per-app sizes and the whole-backup total
// reconcile". It does not say WHICH domains are an app's, and that is the decision this file
// makes. Rung-local, inside a ruling D3 already took; flagged in the PR so review can
// overrule it cheaply, which is the shape D10 uses for its own taste call.
//
// EVERY DOMAIN IS COUNTED EXACTLY ONCE. That is not tidiness — it is what makes G3 assertable
// at all. A domain attributed to two apps inflates the total; one attributed to none and not
// swept into the remainder deflates it. Either way the screen shows a set of numbers that do
// not add up, which is the "no silent caps" rule failing in its arithmetic form.

// The iOS domain prefixes that name an app. Both are per-app containers.
const APP_DOMAIN = "AppDomain-";
const APP_PLUGIN_DOMAIN = "AppDomainPlugin-";

// AppDomainGroup- IS DELIBERATELY NOT HERE, and it is the interesting exclusion. A group
// container is SHARED between apps by design — that is what an app group is — so attributing
// its bytes to any one of them would be a guess presented as a measurement, and attributing
// them to several would double-count and break the reconciliation. It goes to the remainder,
// where it is disclosed as "not attributed to a single app" rather than silently dropped.

export interface AppSize {
  bundleID: string;
  files: number;
  bytes: number;
  // domains is how many domain rows were folded into this app — 0 means the app is in
  // Info.plist and has NO data in this backup, which is a real and different fact from a
  // small app. The surface renders those two differently.
  domains: number;
}

export interface Remainder {
  files: number;
  bytes: number;
  domains: number;
}

export interface Partition {
  apps: AppSize[];
  remainder: Remainder;
  // totals over everything the partition saw, so a caller can check its own arithmetic
  // against the server's Totals rather than trusting either (G3).
  totals: { files: number; bytes: number; domains: number };
}

// partitionByApp folds domain rows into the user-installed apps, with everything else in the
// remainder.
//
// LONGEST MATCH WINS, and without it this is wrong on real data. Bundle ids nest: an app
// `com.example.notes` and its own plugin bundle `com.example.notes.helper` are both in the
// installed list, and `AppDomainPlugin-com.example.notes.helper` starts with BOTH. Taking the
// first match would attribute the plugin's bytes to the parent app, which is a silent
// misattribution rather than a visible failure.
//
// A MATCH MUST END ON A BOUNDARY. `com.example.notes` must not claim
// `AppDomain-com.example.notesomething` — a different app whose id merely starts the same way.
// So the character after the id has to be a dot, or the id has to be the whole of it.
export function partitionByApp(bundleIDs: string[], rows: DomainSummary[]): Partition {
  // Longest first, so the first match found is the most specific one.
  const ids = [...bundleIDs].sort((a, b) => b.length - a.length);

  const byID = new Map<string, AppSize>();
  for (const id of bundleIDs) {
    byID.set(id, { bundleID: id, files: 0, bytes: 0, domains: 0 });
  }
  const remainder: Remainder = { files: 0, bytes: 0, domains: 0 };
  const totals = { files: 0, bytes: 0, domains: 0 };

  for (const row of rows) {
    totals.files += row.files;
    totals.bytes += row.bytes;
    totals.domains += 1;

    const owner = ownerOf(row.domain, ids);
    const app = owner === null ? undefined : byID.get(owner);
    if (app === undefined) {
      remainder.files += row.files;
      remainder.bytes += row.bytes;
      remainder.domains += 1;
      continue;
    }
    app.files += row.files;
    app.bytes += row.bytes;
    app.domains += 1;
  }

  // Biggest first: the question the screen answers is "what is taking the space".
  const apps = [...byID.values()].sort((a, b) => b.bytes - a.bytes || a.bundleID.localeCompare(b.bundleID));
  return { apps, remainder, totals };
}

// ownerOf returns the bundle id a domain belongs to, or null for one that belongs to no
// single app. `ids` must be sorted longest-first.
function ownerOf(domain: string, ids: string[]): string | null {
  let rest: string | null = null;
  if (domain.startsWith(APP_DOMAIN)) {
    rest = domain.slice(APP_DOMAIN.length);
  } else if (domain.startsWith(APP_PLUGIN_DOMAIN)) {
    rest = domain.slice(APP_PLUGIN_DOMAIN.length);
  }
  if (rest === null) return null;

  for (const id of ids) {
    if (rest === id) return id;
    // The boundary check: `.` after the id, never a bare prefix.
    if (rest.startsWith(id) && rest.charCodeAt(id.length) === 46) return id;
  }
  // AN APP DOMAIN FOR AN APP THAT IS NOT IN THE INSTALLED LIST IS REMAINDER, NOT A NEW APP.
  // D3 rules that "apps" means Info.plist's 21, and a backup holds app domains for bundles
  // that list does not name — Apple's own services among them. Inventing a row for one would
  // silently answer a different question from the one the label promises.
  return null;
}
