// withoutWebAuthn runs `body` in an environment where the browser exposes no WebAuthn — which is
// what plain http looks like to `webauthnAvailable()` (quince#1076).
//
// IT EXISTS BECAUSE THE DEFAULT IS THE OTHER WAY ROUND. `src/test/setup.ts` defines
// `PublicKeyCredential` for the whole suite, since almost every test here is about a working https
// install; the unavailable case is the exception and has to ask for it.
//
// IT IS ASYNC, AND THAT IS NOT A CONVENIENCE. `webauthnAvailable()` is read during RENDER, and a
// component behind react-query renders at least twice — once loading, once with data. A synchronous
// scope that wrapped only the `render()` call would restore the global before the second render, so
// the assertion would run against a tree that had already re-rendered as if https were on. Await the
// whole body, findBy* included.
//
// THE RESTORE IS IN A `finally` AND THAT IS THE OTHER HALF. A leak does not fail the test that
// leaked — it hides the passkey controls from every test after it, which surfaces as unrelated
// assertions failing for a reason nothing in them names.
export async function withoutWebAuthn(body: () => void | Promise<void>): Promise<void> {
  const saved = window.PublicKeyCredential;
  // @ts-expect-error — removing a lib.dom global is the whole point of this helper.
  delete window.PublicKeyCredential;
  try {
    await body();
  } finally {
    Object.defineProperty(window, "PublicKeyCredential", {
      value: saved,
      configurable: true,
      writable: true,
    });
  }
}
