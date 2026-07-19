#!/bin/sh
# Runs inside the official Playwright image (glibc + browsers preinstalled). Installs the
# UI deps into an isolated node_modules volume (so the alpine gate install isn't reused
# across libc), waits for the demo app, then runs the Playwright specs. Invoked by the
# Makefile gates-ui-e2e target; BASE_URL + PNPM_VERSION come from the environment.
set -eu

corepack enable >/dev/null 2>&1 || true
corepack prepare "pnpm@${PNPM_VERSION:-9.15.0}" --activate >/dev/null 2>&1 || true

pnpm install --frozen-lockfile --store-dir /pnpm-store

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
