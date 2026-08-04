#!/bin/sh
# Runs inside the official Playwright image (glibc + browsers preinstalled). Installs the
# UI deps into an isolated node_modules volume (so the alpine gate install isn't reused
# across libc), waits for the demo app, then runs the Playwright specs. Invoked by the
# Makefile gates-ui-e2e target; BASE_URL + PNPM_VERSION come from the environment.
set -eu

corepack enable >/dev/null 2>&1 || true
corepack prepare "pnpm@${PNPM_VERSION:-9.15.0}" --activate >/dev/null 2>&1 || true

pnpm install --frozen-lockfile --store-dir /pnpm-store

# BOTH app containers, not just BASE_URL. The `--public-demo` app (quince#534) starts alongside the
# `--demo` one and story 9 hits it directly, so without this wait the suite races a container that is
# still booting — and that presents as a FLAKE, which is the most expensive possible disguise for a
# real defect. Waiting costs nothing: a demo mode reaches "quince serving" in tens of milliseconds.
#
# An unset PUBLIC_DEMO_URL is deliberately NOT an error here. The spec itself refuses in that case,
# with a message naming what did not get checked — one refusal, in the place that knows what it was
# guarding, rather than two that can disagree about whether the run was valid.
wait_for_health() {
  echo "waiting for ${1}/api/health …"
  QUINCE_WAIT_URL="$1" node -e '
    const url = process.env.QUINCE_WAIT_URL + "/api/health";
    const start = Date.now();
    (function poll() {
      fetch(url)
        .then((r) => process.exit(r.ok ? 0 : 1))
        .catch(() => {
          if (Date.now() - start > 60000) {
            console.error("timed out waiting for " + url);
            process.exit(1);
          }
          setTimeout(poll, 1000);
        });
    })();
  '
}

wait_for_health "$BASE_URL"
if [ -n "${PUBLIC_DEMO_URL:-}" ]; then
  wait_for_health "$PUBLIC_DEMO_URL"
fi

pnpm exec playwright test
