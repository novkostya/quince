import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import type { DomainCapability } from "@/lib/types";
import { DomainReport } from "./DomainReport";

// STORY 5 — four states, four remedies, and the assertions here are about them being
// DISTINGUISHABLE rather than merely present. A report that renders all four alike would
// satisfy a test that only checked each row exists.

const FINGERPRINT = "invented-fingerprint-0001";

function row(over: Partial<DomainCapability> = {}): DomainCapability {
  return { domain: "NotesDomain", state: "supported", ...over };
}

describe("DomainReport", () => {
  it("renders nothing when the endpoint carried no report", () => {
    const { container } = render(<DomainReport domains={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  // THE FOUR LABELS ARE DIFFERENT. This is the assertion the whole component exists for: if
  // two states ever render the same words, the collapse D6 forbids has happened.
  it("gives each of the four states its own label", () => {
    const states: DomainCapability["state"][] = [
      "supported", "unsupported_schema", "absent", "unreadable",
    ];
    render(
      <DomainReport
        domains={states.map((s, i) => row({ domain: `Domain${i}`, state: s }))}
      />,
    );
    const labels = ["Readable", "Unrecognised format", "Not in this backup", "Damaged"];
    for (const l of labels) expect(screen.getByText(l)).toBeInTheDocument();
    expect(new Set(labels).size).toBe(4);
  });

  // AN UNRECOGNISED SCHEMA CARRIES ITS FINGERPRINT, which is the actionable part — without it
  // "unsupported" is a dead end for whoever would add support.
  it("shows the fingerprint on an unrecognised schema", () => {
    render(
      <DomainReport domains={[row({ state: "unsupported_schema", fingerprint: FINGERPRINT })]} />,
    );
    expect(screen.getByText(FINGERPRINT)).toBeInTheDocument();
    expect(screen.getByText(/lets support be added/)).toBeInTheDocument();
  });

  // ABSENT IS NOT A FAULT, and the copy must not imply one. A user who never used an app
  // sees exactly this.
  it("says an absent database is not a fault", () => {
    render(<DomainReport domains={[row({ state: "absent" })]} />);
    expect(screen.getByText(/nothing is wrong/)).toBeInTheDocument();
  });

  // UNREADABLE MUST NOT INVITE A SCHEMA ISSUE. This is the remedy split D6 names: filing
  // against a corrupt file wastes everybody's time, so the copy says so explicitly.
  it("tells the user that format support would not fix a damaged database", () => {
    render(<DomainReport domains={[row({ state: "unreadable" })]} />);
    expect(screen.getByText(/would not help/)).toBeInTheDocument();
    // And it must NOT carry the schema-issue invitation the neighbouring state carries.
    expect(screen.queryByText(/lets support be added/)).not.toBeInTheDocument();
  });

  // `missing` NAMES THE FIELDS. A count would be the collapse — "no silent caps" as a data
  // structure means enumerating what cannot be provided, not tallying it.
  it("names the fields a supported schema cannot provide", () => {
    render(
      <DomainReport
        domains={[row({ state: "supported", schema: "notes-v3", missing: ["folders", "pins"] })]}
      />,
    );
    expect(screen.getByText(/folders, pins/)).toBeInTheDocument();
  });

  it("says so when a supported schema is missing nothing", () => {
    render(<DomainReport domains={[row({ state: "supported", schema: "notes-v3" })]} />);
    expect(screen.getByText(/Nothing is missing/)).toBeInTheDocument();
  });

  // D9 — whatever this report says, the browser reaches the bytes. Stated on the surface so
  // an unsupported domain does not read as "this data is unreachable".
  it("says the file browser reaches everything regardless", () => {
    render(<DomainReport domains={[row({ state: "unsupported_schema" })]} />);
    expect(screen.getByText(/file browser reaches everything/)).toBeInTheDocument();
  });
});
