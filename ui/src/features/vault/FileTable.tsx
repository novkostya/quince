import type { VaultFileEntry } from "@/lib/types";
import { formatBytes } from "@/lib/format";
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
export function FileTable({ entries }: { entries: VaultFileEntry[] }) {
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
            <div className="mt-0.5 truncate text-xs text-muted" title={e.domain}>
              {e.domain}
            </div>
          </div>
          <div className="shrink-0 text-right">
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
        </li>
      ))}
    </ul>
  );
}
