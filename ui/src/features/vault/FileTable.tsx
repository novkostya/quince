import { Download } from "lucide-react";
import type { VaultFileEntry } from "@/lib/types";
import { formatBytes } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { RelativeTime } from "@/components/RelativeTime";

// FileTable renders one page-set of browse rows (contracts §2's FileEntry).
//
// THE PATH AND THE DOMAIN ARE BOTH SHOWN, and that is a decision the download route depends on.
// `Content-Disposition` carries the BASENAME only, always and never conditionally — quince#1397
// ruled against disambiguating on collision, on the grounds that a file's saved name would then
// depend on what else you had already downloaded. The cost of that ruling is that a user with two
// files called `Info.plist` cannot tell them apart from the saved file, so the row is where they
// learn which one they took. Dropping either column here to save width would make the download
// ambiguous somewhere else.
//
// NO VIRTUALIZATION, and this is a bounded claim rather than an oversight. ui.design.md §3 asks for
// virtualized data-dense views; a page here is the server's default 500 rows and the list grows only
// when the reader asks for more, so what is mounted is what was requested. That stops being true if
// a page size ever becomes large or automatic, which is the condition to watch rather than a row
// count to fear.
export function FileTable({ entries, sessionID }: { entries: VaultFileEntry[]; sessionID: string }) {
  return (
    <ul className="flex flex-col divide-y divide-line rounded-card border border-line bg-card">
      {entries.map((e) => (
        <li key={e.file_id} className="flex items-baseline justify-between gap-4 px-4 py-2">
          <div className="min-w-0">
            {/* AN EMPTY `relative_path` IS THE DOMAIN'S OWN FOLDER, and rendering it as an empty
                line is what a real backup does to this row. Measured on the stand, 2026-08-21: the
                first page of a real encrypted iPad version is 500 rows of which **99 carry an empty
                path** — one per domain, every one a `dir` of size 0. No fixture had it, because a
                fixture author writes the rows they are thinking about.

                The domain is on the line below either way, so this says what the row IS rather
                than repeating the name. */}
            <div className="truncate text-sm" title={e.relative_path || e.domain}>
              {e.relative_path || <span className="text-muted">the domain&rsquo;s own folder</span>}
            </div>
            <div className="mt-0.5 flex flex-wrap items-center gap-2">
              <span className="truncate text-xs text-muted" title={e.domain}>
                {e.domain}
              </span>
              {/* PRESENT ONLY WHEN TRUE, AND THERE IS NO OPPOSITE BADGE. Absence means "nothing to
                  report", NEVER "checked and clean" — quince#1379's review is explicit that two
                  different things produce an absent field, the second being that quince did not
                  look. A green "complete" chip here would turn "we have not read this file" into a
                  clean bill of health on every row of every backup.

                  THE WORDS ARE ABOVE THE LIST, NOT IN A `title`. A tooltip is unreachable on the
                  device this product is mostly read on, and these conditions run to tens of files
                  per version — a sentence per row would bury the list it is explaining. */}
              {e.incomplete ? <Badge tone="danger">incomplete</Badge> : null}
              {e.overlong ? <Badge tone="warn">overlong</Badge> : null}
            </div>
          </div>
          <div className="flex shrink-0 items-baseline gap-3 text-right">
            <div>
              {/* A DIRECTORY HAS NO SIZE WORTH PRINTING. The record carries one and it is not the
                  size of anything a user can act on, so the kind is shown instead — `formatBytes(0)`
                  would read as an empty file, which a directory is not. */}
              <div className="font-mono text-xs tabular-nums text-subtle">
                {e.kind === "file" ? formatBytes(e.size) : e.kind}
              </div>
              {/* An EMPTY mtime is ordinary rather than corrupt: LastModified is optional in the
                  backup format (types.ts). RelativeTime already renders an em dash for "", which is
                  why this is not guarded here — rendering 1 January 1970 is the failure to avoid. */}
              <RelativeTime iso={e.mtime} className="text-xs text-muted" />
            </div>
            {/* AN ORDINARY LINK, NOT A FETCH-AND-BLOB. A backup file can be gigabytes; pulling one
                through script to build an object URL holds the whole thing in the tab's memory for
                no gain. The browser streams it to disk, shows its own progress, and resumes nothing
                script would have had to reimplement.

                NO `download` ATTRIBUTE. The server sets `Content-Disposition: attachment` with a
                sanitized basename and that is a SECURITY control rather than a convenience
                (contracts §1) — a backup holds arbitrary HTML and SVG, and `inline` at quince's own
                origin is stored XSS. Adding the attribute would make the client a second naming
                authority beside the header, and the two could disagree.

                OFFERED ON FILES ONLY. `Open` on a directory or a symlink answers `not_a_file`
                (story 7), so a control here would be a button whose only outcome is a refusal.
                A PLAIN NAVIGATION, WITH NO `target`, AND THAT IS A DECISION RATHER THAN A DEFAULT
                (review, quince#1425). On success `Content-Disposition: attachment` means the
                browser downloads and this page survives. **On a FAILURE there is no such header**,
                so the browser renders the JSON body and the browse page is gone — list, filter and
                session with it. The reachable case is a session that expired between load and
                click, which is the one case this page otherwise handles well.

                `target="_blank" rel="noopener"` would keep the page and put the failure in a tab
                the user closes. It is NOT taken, for two reasons and one absence:

                  - the cost lands on the COMMON path. Every successful download opens a tab, and
                    on some engines it stays blank. This product is read on a phone, so that is a
                    certain repeated annoyance traded against an occasional one;
                  - the failure is RECOVERABLE. Back returns here, and a fresh mount holds no
                    session, so the reader lands on the locked panel with an Open button — the
                    same place an expiry sends them anyway;
                  - and WHICH engines leave the tab blank is not measured. quince#1405 is exactly
                    that measurement — `Content-Disposition` rendering across Chrome, Firefox and
                    WebKit — and it is blocked on the e2e rig being chromium-only. Choosing
                    `_blank` today would be choosing on a guess about the browser this product is
                    mostly used in.

                What is genuinely poor here is the raw JSON error document; it is a surface no
                other part of this page would accept. If quince#1405 lands and shows the tab is
                closed cleanly on WebKit, revisit this.
            */}
            {e.kind === "file" ? (
              <a
                href={`/api/sessions/${sessionID}/file/${e.file_id}`}
                aria-label={`Download ${e.relative_path}`}
                className="rounded-lg p-1 text-subtle transition-colors hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
              >
                <Download size={16} aria-hidden />
              </a>
            ) : null}
          </div>
        </li>
      ))}
    </ul>
  );
}
