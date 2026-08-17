# Cutting a quince release

Push a tag; the forge does the rest. This file is what makes that reproducible without reading
`.github/workflows/release.yml`, and it records the two things a tag does **not** do.

---

## The short version

```sh
git tag -a v0.1.0 -m 'quince 0.1.0'
git push origin v0.1.0
```

That fires `release.yml`, which builds `linux/amd64` and `linux/arm64` **natively, in parallel**,
publishes them to `ghcr.io/novkostya/quince`, joins them into one multi-arch tag, and opens a GitHub
Release with generated notes.

Afterwards:

```sh
docker pull ghcr.io/novkostya/quince:0.1.0     # or :latest
```

## The version rules, all three

**The tag carries `v`; nothing else does.** `v0.1.0` is the git tag, `0.1.0` is the image tag and the
version the binary reports. The workflow strips it in exactly one place and `deploy/release-image`
refuses a version that still has it, naming the corrected string.

**Three components, always.** `v0.1` is refused. So would `v1` be. The pipeline is semver-shaped
because `:latest` and pre-release handling both key off it.

**A `-` makes it a pre-release**, and that changes two things together: the image does **not** move
`:latest`, and the GitHub Release is marked pre-release. `v0.2.0-rc1` publishes `:0.2.0-rc1` and
nothing else. This is the single most consequential rule here — `docker pull quince` resolves to
`:latest`, so a release candidate that moved it would reach everyone who did not name a version.

## What a wrong tag does — it fails LOUDLY, which is deliberate

The trigger is `v*`, deliberately wide. A narrow pattern like `v[0-9]+.[0-9]+.[0-9]+` looks tighter
and is worse: **a tag matching no filter fires no workflow at all** — no run, no red X, no
notification — so `v0.1` would be silently ignored and so would `v0.10.0`, an ordinary release. You
would push and learn nothing until someone went looking for a package that was never built.

So every `v*` tag starts a run, and a malformed one dies in its first job in seconds:

```
release-image: 0.1 is not a release version (want 0.1.0, or 0.2.0-rc1).
```

**Delete the bad tag and push a correct one.** Nothing was published, because validation runs before
the build and long before any push:

```sh
git tag -d v0.1 && git push origin :refs/tags/v0.1
```

## Two things the pipeline does NOT do

**1. It cannot make the package public — and that is a one-way door.** GitHub creates a package
private on first publish; it does **not** inherit repository visibility. Until an owner changes it,
`ghcr.io/novkostya/quince` exists and nobody can pull it. GitHub's own warning: *"Once you make a
package public, you cannot make it private again."*

That is an **Operator action**, once, on the package's settings page — no agent seat holds
`administration`.

**2. It does not update the public demo.** `deploy/demo.md` still builds from source on fly's
builder. Switching it to `flyctl deploy --image ghcr.io/...` is quince#615/quince#612, not this
pipeline.

## If a build fails

**Nothing partial is ever pointed at.** The two architecture jobs run with `fail-fast: false`, and
the manifest job runs only if both succeed. So a half-failed release leaves `:0.1.0-amd64` and
possibly `:0.1.0-arm64` in the registry, and **neither `:0.1.0` nor `:latest` exists or moves**.

To retry, delete the tag and push it again — the per-arch tags are simply overwritten:

```sh
git tag -d v0.1.0 && git push origin :refs/tags/v0.1.0
# fix, then re-tag and push
```

Per-arch tags are a deliberate cost of the design, not litter to clean up urgently: they are how the
two native jobs hand their results to the manifest job without artifact plumbing between them.

## Running a build by hand

`deploy/release-image` is an ordinary script and needs no forge:

```sh
docker login ghcr.io                          # credentials are the caller's; the script reads none
deploy/release-image build    0.1.0 amd64     # one architecture, pushed as :0.1.0-amd64
deploy/release-image manifest 0.1.0 amd64 arm64
```

**It refuses to build an architecture this host is not**, rather than falling back to QEMU emulation
— a wrong architecture would otherwise cost an hour of emulated C compilation, or fail deep inside a
build stage for reasons that read as a source bug. `QUINCE_RELEASE_ALLOW_FOREIGN_ARCH=1` overrides
and announces itself. `QUINCE_IMAGE` and `QUINCE_RUNTIME` override the registry and the runtime.

Its refusals are covered by `make release-image-test`.

## Why this file does not contain the workflow

`deploy/demo.md` carries `demo-deploy.yml` verbatim under a gate (`make demo-block-check`), because
that file had to be installed by hand by someone who could not have written it. **That reason is
gone** — `quince-coder` holds `workflows: write` as of 2026-08-17 and pushed `release.yml` itself. A
second verbatim copy here would be duplication needing a second gate to keep honest, so this file
points at `.github/workflows/release.yml` and stops.

## Unproven, as of the first release

**The `linux/arm64` build has never run.** All three pinned base images publish arm64 and the image
carries no Rust or Python stage, so the surface is Go, Node and the C libimobiledevice build on
Alpine arm64 — none of it executed. The first tagged run is the first measurement. If it fails,
amd64 still publishes its per-arch tag and no multi-arch tag is created, so nothing broken is
pointed at.

**Publishing has had no security review.** *"The fixtures contain no real data"* is a different claim
from *"this image is safe to publish"*, and this is the project's first artifact on a public
registry. That review is owed before the visibility flip above, not after.
