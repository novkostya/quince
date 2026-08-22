import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import type { DomainCapability } from "@/lib/types";

// DomainReport renders D6's capability report — story 5.
//
// FOUR STATES, AND THE WHOLE POINT IS THAT THEY DO NOT COLLAPSE. Each row answers a different
// question and, more importantly, implies a different REMEDY:
//
//   supported           quince can read this. Nothing to do.
//   unsupported_schema  the database is here and quince does not know this schema. A schema
//                       -support issue is the remedy, and the FINGERPRINT is what it needs.
//   absent              there is no such database in this backup. NOT a defect at all — a
//                       user with no data for a domain looks exactly like this.
//   unreadable          the database is here and damaged. No parser work will help.
//
// Folding `unreadable` into `unsupported_schema` would send somebody to file an issue against
// a corrupt file; folding `absent` into either would tell them quince failed when nobody
// failed. That is the *troubleshooting is ACTIONABLE* rule in its negative form — a
// diagnostic that collapses distinguishable causes is a defect even when every word is true.
//
// A DOMAIN QUINCE CANNOT REACH IS NOT IN THIS LIST AT ALL, and that is deliberate upstream:
// `absent` means *not in this backup*, a fact about the user's data, whereas "quince has no
// support compiled in" is a fact about quince. Reporting the second as the first would tell
// somebody they have no Safari data when nobody looked. So this component must never render
// an empty row for a domain it expected and did not find — the report is the list.

const TONE: Record<DomainCapability["state"], "ok" | "warn" | "neutral" | "danger"> = {
  supported: "ok",
  unsupported_schema: "warn",
  // NEUTRAL, NOT A WARNING. Absent is not a fault, and colouring it like one would make a
  // user with no Notes data think something is broken.
  absent: "neutral",
  unreadable: "danger",
};

const LABEL: Record<DomainCapability["state"], string> = {
  supported: "Readable",
  unsupported_schema: "Unrecognised format",
  absent: "Not in this backup",
  unreadable: "Damaged",
};

export function DomainReport({ domains }: { domains: DomainCapability[] | undefined }) {
  // ABSENT KEY, NOT AN EMPTY LIST, is how an endpoint says it carries no report — so there is
  // nothing to render rather than an empty section implying quince found nothing.
  if (domains === undefined || domains.length === 0) return null;

  return (
    <Card>
      <h3 className="text-sm font-medium text-fg">What quince can read from this backup</h3>
      <p className="mt-1 text-xs text-muted">
        The file browser reaches everything below, whatever this says.
      </p>
      <ul className="mt-3 flex flex-col gap-2">
        {domains.map((d) => (
          <li key={d.domain} className="border-t border-line pt-2 first:border-0 first:pt-0">
            <div className="flex items-baseline justify-between gap-3">
              <span className="text-sm text-fg">{d.domain}</span>
              <Badge tone={TONE[d.state]}>{LABEL[d.state]}</Badge>
            </div>
            <Detail domain={d} />
          </li>
        ))}
      </ul>
    </Card>
  );
}

// Detail says what to DO, per state. Every branch names a remedy or says explicitly that
// none is needed — a row that only names a state leaves the reader to guess which of the four
// situations they are in, which is the collapse this report exists to prevent.
function Detail({ domain: d }: { domain: DomainCapability }) {
  switch (d.state) {
    case "supported":
      return (
        <div className="mt-1 text-xs text-muted">
          {d.schema ? <>Recognised as {d.schema}. </> : null}
          {d.missing && d.missing.length > 0 ? (
            // `Missing` IS "no silent caps" AS A DATA STRUCTURE — it enumerates what this
            // schema cannot give rather than returning empties and letting a viewer show
            // blanks. Naming the fields is the whole value; a count would be the collapse.
            <>
              This version of the format does not carry:{" "}
              <span className="text-fg">{d.missing.join(", ")}</span>. Everything else is
              readable.
            </>
          ) : (
            <>Nothing is missing from this one.</>
          )}
        </div>
      );
    case "unsupported_schema":
      return (
        <div className="mt-1 text-xs text-muted">
          The database is in this backup and quince does not recognise its layout — usually a
          newer iOS than this version of quince knows about.{" "}
          <span className="text-fg">
            Reporting it with the fingerprint below is what lets support be added.
          </span>
          {d.fingerprint ? (
            // THE FINGERPRINT IS THE ACTIONABLE PART, not decoration. Without it
            // "unsupported" is a dead end for whoever would add support, which is exactly
            // why D6 splits this state from `unreadable`.
            <div className="mt-1 break-all font-mono text-[11px] text-subtle">
              {d.fingerprint}
            </div>
          ) : null}
        </div>
      );
    case "absent":
      return (
        <div className="mt-1 text-xs text-muted">
          There is no such database in this backup. If you have never used this app, that is
          what this looks like — nothing is wrong.
        </div>
      );
    case "unreadable":
      return (
        <div className="mt-1 text-xs text-muted">
          The database is in this backup and could not be opened at all — damaged, or not a
          database. <span className="text-fg">Adding format support would not help.</span> The
          file browser can still download the raw file.
        </div>
      );
  }
}
