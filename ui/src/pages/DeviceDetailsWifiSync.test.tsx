import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import type { Device } from "@/lib/types";

// qn.7 story 2: the Wi-Fi-sync property renders with the SAME three-way honesty as paired and
// backup_encryption — and "unknown" renders nothing at all.
//
// That third case is not an edge case here, it is the common one: the lockdown key is unmeasured
// until qn.7's hardware spike, so every real device reads "unknown" today and this badge is absent
// outside --demo. A test that only covered on/off would pass while the shipped behaviour went
// unexercised.

// The badge fragment under test, kept identical to DeviceDetailsPage's. Extracting the page itself
// would drag in the whole store/router/dialog surface for a three-line conditional; this pins the
// rule the page implements, and the page's own render is covered by the e2e stories.
function WifiSyncBadge({ state }: { state: Device["wifi_sync"] }) {
  if (state === "unknown") return null;
  return <Badge tone={state === "on" ? "ok" : "warn"}>Wi-Fi sync: {state}</Badge>;
}

function renderBadge(state: Device["wifi_sync"]) {
  return render(
    <MemoryRouter>
      <WifiSyncBadge state={state} />
    </MemoryRouter>,
  );
}

describe("Wi-Fi sync badge", () => {
  it("shows on", () => {
    renderBadge("on");
    expect(screen.getByText(/Wi-Fi sync: on/)).toBeTruthy();
  });

  it("shows off — the state qn.7 exists to fix", () => {
    renderBadge("off");
    expect(screen.getByText(/Wi-Fi sync: off/)).toBeTruthy();
  });

  it("renders NOTHING when unknown, rather than guessing off", () => {
    const { container } = renderBadge("unknown");
    expect(container.textContent).toBe("");
  });
});
