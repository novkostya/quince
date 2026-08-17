import "@testing-library/jest-dom/vitest";

// jsdom EXPOSES NO `PublicKeyCredential`, WHICH MAKES EVERY TEST RUN LOOK LIKE PLAIN HTTP
// (quince#1076). WebAuthn is secure-context-only, so `webauthnAvailable()` — the one expression
// every passkey control now consults — answers false in jsdom for the same reason it answers false
// on `http://<ip>:<port>`.
//
// THAT IS THE WRONG DEFAULT FOR THIS SUITE. Almost every test here is about what a signed-in user
// sees on a working install, which is https; leaving it undefined would hide the passkey controls
// from all of them and turn a suite about behaviour into a suite about the environment.
//
// So the DEFAULT is available, declared here once, and the tests that are about the unavailable case
// delete it explicitly and say so. A stub object is enough: nothing in the UI calls a method on this
// constructor — `navigator.credentials` is what runs a ceremony, and every test that reaches one
// mocks `@/lib/webauthn` wholesale.
if (typeof window !== "undefined" && typeof window.PublicKeyCredential === "undefined") {
  Object.defineProperty(window, "PublicKeyCredential", {
    value: function PublicKeyCredential() {},
    configurable: true,
    writable: true,
  });
}
