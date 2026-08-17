#!/bin/sh
# Runs inside the official Playwright image (glibc + browsers preinstalled). Installs the
# UI deps into an isolated node_modules volume (so the alpine gate install isn't reused
# across libc), waits for the demo app, then runs the Playwright specs. Invoked by the
# Makefile gates-ui-e2e target; BASE_URL + PNPM_VERSION come from the environment.
set -eu

corepack enable >/dev/null 2>&1 || true
corepack prepare "pnpm@${PNPM_VERSION:-9.15.0}" --activate >/dev/null 2>&1 || true

pnpm install --frozen-lockfile --store-dir /pnpm-store

# THE MEASUREMENT PROBE'S OWN GROUND-TRUTH CHECK, FIRST AND FAST (quince#1155).
#
# `ui/measure/probe.mjs` produces the numbers `docs/ui.type-survey.md` argues from and that
# `ui.design.md` sets contrast and type floors against. Re-running a sweep proves the probe is
# CONSISTENT; it cannot prove it measures the right thing, because a probe that is consistently
# wrong reproduces perfectly. This runs it against a fixture where every size, colour and blend is
# declared, so each claim has an answer that is right or wrong with no judgement in between.
#
# HERE RATHER THAN IN `gates-ui` BECAUSE IT NEEDS A BROWSER, and this is the gate that already has
# one — no new make target and no gate-map entry, since `e2e` already scopes to `ui/`. It runs
# BEFORE the wait below because it needs no demo server, so a broken probe fails in seconds rather
# than after a 60-second health poll.
echo "validating the measurement probe against declared ground truth …"
node measure/validate.mjs

echo "waiting for ${BASE_URL}/api/health …"
node -e '
  const url = process.env.BASE_URL + "/api/health";
  const start = Date.now();
  (function poll() {
    fetch(url)
      .then((r) => process.exit(r.ok ? 0 : 1))
      .catch(() => {
        if (Date.now() - start > 60000) {
          console.error("timed out waiting for the demo app");
          process.exit(1);
        }
        setTimeout(poll, 1000);
      });
  })();
'

pnpm exec playwright test
