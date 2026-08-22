import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import type { VersionOverview } from "@/lib/types";
import { VersionSummary } from "./VersionSummary";

// EVERY IDENTIFIER HERE IS INVENTED (spec D8/D10).

function ov(over: Partial<VersionOverview> = {}): VersionOverview {
  return {
    version_id: "V1",
    udid: "DEV-1",
    encrypted: true,
    created_at: "2026-07-20T00:00:00Z",
    kind: "full",
    file_count: null,
    device: {
      present: true,
      name: "Study Tablet",
      ios_version: "17.5.1",
      class: "iPad",
      product_type: "iPadT9,9",
      build_version: "21F9000",
      serial_number: "SERIALINVENTED1",
      unique_device_id: "00009999-000A9999A99A999A",
    },
    backup: {
      present: true,
      state: "new",
      snapshot_state: "finished",
      date: "2026-07-20T00:00:00Z",
      uuid: "UUIDINVENTED0001",
      format_version: "3.3",
    },
    apps: {
      present: true,
      bundle_ids: ["com.example.notes", "com.example.reader"],
      display_name: "Study Tablet",
      itunes_version: "12.12.9",
      last_backup_date: "2026-07-20T00:00:00Z",
      cellular: { imei: "", iccid: "", phone_number: "" },
    },
    ...over,
  };
}

describe("VersionSummary", () => {
  // STORY 7 / G2 — the count is unknown before an unlock and must never render as a number.
  it("says the file count is unknown rather than showing a number", () => {
    render(<VersionSummary overview={ov({ file_count: null })} />);
    expect(screen.getByText("unknown")).toBeInTheDocument();
    // The specific hazard: a null rendered through a formatter becomes "0".
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  // D3 — "apps" is one of FOUR counts and the label must say which.
  it("labels the app list as the ones the user installed", () => {
    render(<VersionSummary overview={ov()} />);
    expect(screen.getByText("Apps you installed")).toBeInTheDocument();
    expect(screen.getByText("com.example.notes")).toBeInTheDocument();
  });

  // ABSENT IS NOT EMPTY. No Info.plist means quince cannot know the list; that is a different
  // statement from a backup holding no apps, and they must not render alike.
  it("distinguishes a missing app list from an empty one", () => {
    const { unmount } = render(
      <VersionSummary overview={ov({ apps: { ...ov().apps, present: false, bundle_ids: [] } })} />,
    );
    expect(screen.getByText(/carries no/)).toBeInTheDocument();
    unmount();

    render(<VersionSummary overview={ov({ apps: { ...ov().apps, present: true, bundle_ids: [] } })} />);
    expect(screen.getByText(/No apps were installed/)).toBeInTheDocument();
    expect(screen.queryByText(/carries no/)).not.toBeInTheDocument();
  });

  // quince#1466 — an adopted version's kind is UNKNOWN and is rendered as itself. The hazard
  // is a screen that fills the gap from Status.plist, which lies.
  it("renders an unrecorded kind as unrecorded rather than guessing", () => {
    render(<VersionSummary overview={ov({ kind: "unknown" })} />);
    expect(screen.getByText("Kind not recorded")).toBeInTheDocument();
    expect(screen.queryByText("Full backup")).not.toBeInTheDocument();
    expect(screen.queryByText("Incremental")).not.toBeInTheDocument();
  });

  // D10 — the identifiers are IN SCOPE and NOT IN THE DEFAULT VIEW.
  it("keeps device identifiers behind a closed disclosure", () => {
    const o = ov({
      apps: {
        ...ov().apps,
        cellular: { imei: "990000000000001", iccid: "89000000000000000001", phone_number: "+15550000001" },
      },
    });
    const { container } = render(<VersionSummary overview={o} />);

    const details = container.querySelector("details");
    expect(details).not.toBeNull();
    // CLOSED IS THE RESTING STATE. `open` here would put an IMEI on the screen by default,
    // which is the whole of what D10 decided against.
    expect(details).not.toHaveAttribute("open");
    // The values are in the DOM (a <details> renders its children) — the assertion is about
    // the disclosure being shut, not about them being absent.
    expect(screen.getByText("990000000000001")).toBeInTheDocument();
  });

  // A tablet has no cellular identifiers, so there is nothing to disclose and no control that
  // reveals nothing.
  it("omits the disclosure entirely when there is nothing to disclose", () => {
    const o = ov({
      device: { ...ov().device, serial_number: "", unique_device_id: "" },
      apps: {
        ...ov().apps,
        itunes_version: "",
        cellular: { imei: "", iccid: "", phone_number: "" },
      },
    });
    const { container } = render(<VersionSummary overview={o} />);
    expect(container.querySelector("details")).toBeNull();
  });

  // A version whose manifest is missing says so, rather than showing a blank device.
  it("says the device is unknown when the backup carries no manifest", () => {
    render(<VersionSummary overview={ov({ device: { ...ov().device, present: false } })} />);
    expect(screen.getAllByText("unknown").length).toBeGreaterThan(0);
  });

  // The FORMAT version is not the iOS version, and the label must not let them be confused.
  it("names the backup format rather than calling it a version", () => {
    render(<VersionSummary overview={ov()} />);
    expect(screen.getByText("Backup format")).toBeInTheDocument();
  });
});
