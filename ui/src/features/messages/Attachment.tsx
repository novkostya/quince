import * as React from "react";
import type { MessagesAttachment } from "@/lib/types";

// Attachment renders one message attachment — qn.10 slice 7d, stories 4 and 5.
//
// NO NEW FILE-SERVING SURFACE (D6). The bytes come from `qn.8`'s download route addressed by
// (domain, relative_path), which slice 5 added to the SAME handler that serves a file id. This
// component builds the join and nothing else.
//
// COOKIE AUTH IS WHY A BARE <img src> WORKS. The API is `credentials: "same-origin"` and CSRF is
// required only for mutating methods, so the browser sends the session cookie on a GET it issues
// itself. No blob fetch, no object URL to revoke.

// THE ROUTE SERVES octet-stream + nosniff + attachment, AND AN <img> DECODES IT ANYWAY. Measured
// 2026-08-23 across chromium, firefox and webkit, each with an `image/png` control alongside
// (qn.10 D6). Worth knowing before touching this: an `<img>` here is being asked to decode bytes
// declared as a non-image type by a server that said "do not guess", and the reasonable
// expectation is that it refuses. It does not. `ui/e2e/story13-attachment-decodes.spec.ts` keeps
// that true, because if it stopped being true the `onError` fallback below would turn a TOTAL
// failure into "every attachment is a link" with every unit test still passing.
//
// THIS DOES NOT REOPEN quince#1397, WHICH RULED `inline` OUT. That ruling is about serving backup
// content with a REAL content type inside quince's origin, where an SVG or HTML file from a
// backup executes script with the session cookie in scope. No header changes here — the type
// stays `application/octet-stream` and the disposition stays `attachment`. An `<img>` decodes
// bytes and executes no script, not even for SVG, and RENDERABLE is raster-only so SVG never
// reaches it. The surface quince#1397 closed stays closed.
// RENDERABLE is an ALLOWLIST, and the reason is HEIC. iOS backups are full of `image/heic`, and
// no browser except Safari renders it — an <img> pointed at one shows a broken-image icon, which
// is a screen claiming the photo is damaged when it is fine and simply not displayable here.
//
// So the test is not "is this an image" but "will this browser draw it". Everything else becomes a
// named link, which always works. An allowlist fails toward the honest outcome; a `startsWith
// ("image/")` check fails toward the lying one.
const RENDERABLE = new Set(["image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp"]);

// fileURL addresses the attachment through the session's file route.
//
// BOTH PARTS ENCODED: a relative path holds spaces and non-ASCII routinely, and a domain can too.
export function fileURL(sessionID: string, a: MessagesAttachment): string {
  const p = new URLSearchParams({
    domain: a.domain ?? "",
    relative_path: a.relative_path ?? "",
  });
  return `/api/sessions/${encodeURIComponent(sessionID)}/file?${p.toString()}`;
}

// label is what to call the attachment when there is no picture to show.
//
// THE NAME, THEN THE TYPE, THEN THE WORD "attachment" — each step is a real state rather than
// defensive padding: `name` is the filename as sent, which is usually present; `mime_type` at
// least says what kind of thing it is; and neither being there is a record that carries a file
// reference and nothing describing it.
export function label(a: MessagesAttachment): string {
  if (a.name) return a.name;
  if (a.mime_type) return a.mime_type;
  return "attachment";
}

export function Attachment({
  sessionID,
  attachment,
}: {
  sessionID?: string;
  attachment: MessagesAttachment;
}) {
  // A FORMAT THIS BROWSER CANNOT DRAW IS NOT A FAILURE, so falling back is silent. A broken-image
  // icon would be the surface asserting the file is damaged.
  const [drawable, setDrawable] = React.useState(true);

  if (!attachment.present) {
    // NO LINK, AND THE REASON (D6). `present: false` means the backup does not hold the bytes —
    // not downloaded, purged, or iCloud-only. A link here would 404, and offering one that
    // cannot resolve is worse than saying so.
    return (
      <p className="text-xs text-muted">{label(attachment)} — not in this backup</p>
    );
  }

  // WITHOUT A SESSION THERE IS NO URL TO BUILD, so the name renders alone rather than as a dead
  // link. This is the shape a list rendered outside an unlocked page takes, and it is the same
  // rule quince#1518 is about: a control that cannot work must not look like one that can.
  if (!sessionID) {
    return <p className="text-xs text-muted">{label(attachment)}</p>;
  }

  const href = fileURL(sessionID, attachment);

  if (attachment.mime_type && RENDERABLE.has(attachment.mime_type) && drawable) {
    return (
      <a href={href} target="_blank" rel="noreferrer" className="block max-w-xs">
        <img
          src={href}
          // THE FILENAME IS THE ALT TEXT, because it is the only description of the picture that
          // exists. quince has not looked at the image and must not describe it.
          alt={label(attachment)}
          loading="lazy"
          onError={() => setDrawable(false)}
          className="max-h-64 rounded-md border border-line object-contain"
        />
      </a>
    );
  }

  return (
    <p className="text-xs">
      <a
        href={href}
        target="_blank"
        rel="noreferrer"
        className="text-accent hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
      >
        {label(attachment)}
      </a>
      {attachment.sticker && <span className="ml-2 text-muted">sticker</span>}
    </p>
  );
}
