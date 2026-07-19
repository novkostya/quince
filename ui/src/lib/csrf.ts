// Double-submit CSRF: the server sets a readable `quince_csrf` cookie; we echo its value in
// the X-CSRF-Token header on every mutation (auth.CheckCSRF compares them constant-time).
export function readCSRFToken(): string | null {
  const m = document.cookie.match(/(?:^|;\s*)quince_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : null;
}
