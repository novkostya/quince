// DocLink turns a repo path into something a reader can actually open.
//
// PRINTING A FILENAME AT SOMEONE WHO IS STUCK IS A DEAD END DRESSED AS HELP. A user running quince
// from a container image may have no checkout at all, and a phone certainly does not — so
// `deploy/tls.md` as bare text is a reference to a thing they cannot reach.
//
// `blob/main` rather than a pinned commit or tag: the reader wants the CURRENT instructions for the
// quince they are running, and a pinned link rots into describing an older one. The cost is that a
// very old deployment may read newer docs, which is the better failure of the two.
//
// MOVED HERE FROM OnboardingHTTPSPage BY ITS OWN INSTRUCTION. That file said: *"The first external
// link in the UI, so the styling is here rather than in a shared component — one instance is not a
// pattern. If a second page needs it, that is when it moves."* `qn.6e`'s add-storage form is the
// second, and it shipped with two bare `deploy/storage.md` strings before anyone noticed — which is
// what the note was there to prevent.
export function DocLink({ path }: { path: string }) {
  return (
    <a
      className="text-accent underline underline-offset-2"
      href={`https://github.com/novkostya/quince/blob/main/${path}`}
      target="_blank"
      rel="noreferrer"
    >
      {path}
    </a>
  );
}
