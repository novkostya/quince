import { describe, it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Attachment, fileURL, label } from "./Attachment";
import type { MessagesAttachment } from "@/lib/types";

// EVERY IDENTIFIER HERE IS INVENTED (spec D8/D10).

function att(over: Partial<MessagesAttachment> = {}): MessagesAttachment {
  return {
    domain: "MediaDomain",
    relative_path: "Library/SMS/Attachments/ab/01/IMG_0001.JPG",
    mime_type: "image/jpeg",
    name: "IMG_0001.JPG",
    bytes: 1024,
    present: true,
    ...over,
  };
}

describe("fileURL", () => {
  it("addresses qn.8's existing route by domain and path, encoding both", () => {
    const u = fileURL("S1", att({ relative_path: "Library/SMS/a b/é.jpg" }));
    expect(u.startsWith("/api/sessions/S1/file?")).toBe(true);
    // A relative path holds spaces and non-ASCII routinely; an unencoded one truncates the
    // query at the first `&` or breaks on the space.
    expect(u).toMatch(/relative_path=Library%2FSMS%2Fa\+b%2F%C3%A9\.jpg/);
    expect(u).toMatch(/domain=MediaDomain/);
  });
});

describe("label", () => {
  it("prefers the filename, falls back to the type, then to a neutral word", () => {
    expect(label(att())).toBe("IMG_0001.JPG");
    expect(label(att({ name: undefined }))).toBe("image/jpeg");
    expect(label(att({ name: undefined, mime_type: undefined }))).toBe("attachment");
  });
});

describe("Attachment", () => {
  // STORY 5 — the state that must never become a link.
  it("says an absent file is not in this backup, and offers no link", () => {
    render(<Attachment sessionID="S1" attachment={att({ present: false, domain: undefined, relative_path: undefined })} />);
    expect(screen.getByText(/not in this backup/i)).toBeTruthy();
    // A link here would 404. Offering one that cannot resolve is worse than saying so.
    expect(screen.queryByRole("link")).toBeNull();
    expect(document.querySelector("img")).toBeNull();
  });

  // STORY 4 — a photo shows the photo.
  it("draws a jpeg inline, described by its filename", () => {
    render(<Attachment sessionID="S1" attachment={att()} />);
    const img = document.querySelector("img") as HTMLImageElement;
    expect(img).toBeTruthy();
    // THE FILENAME IS THE ALT TEXT. quince has not looked at the image and must not describe it.
    expect(img.getAttribute("alt")).toBe("IMG_0001.JPG");
    expect(img.getAttribute("src")).toMatch(/^\/api\/sessions\/S1\/file\?/);
  });

  // THE ONE THAT MATTERS ON REAL DATA. iOS backups are full of HEIC and no browser but Safari
  // renders it, so an <img> would show a broken-image icon — the surface asserting the photo is
  // damaged when it is fine and simply not displayable here.
  it("offers HEIC as a link rather than a picture the browser cannot draw", () => {
    render(<Attachment sessionID="S1" attachment={att({ mime_type: "image/heic", name: "IMG_0002.HEIC" })} />);
    expect(document.querySelector("img")).toBeNull();
    const link = screen.getByRole("link", { name: "IMG_0002.HEIC" });
    expect(link.getAttribute("href")).toMatch(/^\/api\/sessions\/S1\/file\?/);
  });

  it("offers a non-image as a named link", () => {
    render(<Attachment sessionID="S1" attachment={att({ mime_type: "video/quicktime", name: "IMG_0003.MOV" })} />);
    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByRole("link", { name: "IMG_0003.MOV" })).toBeTruthy();
  });

  // The allowlist is a prediction about the browser; this is what happens when it is wrong.
  it("falls back to a link when a format it expected to draw fails", () => {
    render(<Attachment sessionID="S1" attachment={att()} />);
    const img = document.querySelector("img") as HTMLImageElement;
    fireEvent.error(img);

    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByRole("link", { name: "IMG_0001.JPG" })).toBeTruthy();
  });

  // Rendered outside an unlocked page there is no URL to build, and a dead link would be the
  // shape quince#1518 was filed about.
  it("renders the name alone when there is no session to address", () => {
    render(<Attachment attachment={att()} />);
    expect(screen.queryByRole("link")).toBeNull();
    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText("IMG_0001.JPG")).toBeTruthy();
  });
});
