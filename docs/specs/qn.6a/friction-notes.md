# qn.6a — process friction notes (evidence for the revamp)

> qn.6a is the **last rung under the current process** ((ch)). This file records friction as it is
> hit — letter collisions, doc drift, gate-ownership seams, spec overhead — so the revamp is
> designed from evidence, not memory. Chronological; terse.

- **Onboarding cost is real but front-loaded.** Reconstructing the ruled scope required reading, in
  order: CLAUDE.md → progress one-line-state → the qn.6a dashboard *row* → 7 decision-log entries
  (ch, ci, cj, ck, cr, cu, cv) → roadmap qn.6a block + Later/parked → contracts §1/§2/§6. The
  ruling for a single field (`missing: bool`) is split across (cr), (cv), the dashboard row, AND the
  roadmap block — same fact stated 4×, each slightly differently. **Signal for the revamp:** a rung
  needs ONE canonical scope surface it can trust; today the spec has to re-derive it from a scatter
  of decision letters. The decision log is excellent as *history* but is being used as *spec*.

- **Decision-letter allocation is manual + collision-prone.** "Claim the next free letter, never
  assume" ((bu)) means grepping the whole 1.8k-line progress file to find the highest letter (cz),
  then claiming (da). Fine for one session; fragile with any parallelism. The letters are also
  overloaded: they index BOTH durable rulings AND session build-reports.

- **`local/` is absent from the worktree.** `local/environment.md` (referenced by CLAUDE.md and the
  onboarding brief for the dev loop) does not exist in this git worktree — it is a separate nested
  repo on the Operator's machine. The dev loop (rsync → `quince-dev:~/quince`, gates in pinned
  toolchain containers) had to be rediscovered from the `~/.ssh/config` alias + the remote helper
  scripts (`gt.sh`). A new implementer session in a fresh worktree cannot find the build
  instructions the brief points them to.

- **contracts.md "phase" vs "state" ambiguity.** (cv) ruled a "`seeding` job phase … (contracts
  phase-enum addition)", but the contract has two things a "phase" could mean: the `Job.state`
  enum and the open `progress.phase` string. Resolving it required reading the engine to see that
  lifecycle stages ARE states. A ruled contract shape should name the field it lands on.
