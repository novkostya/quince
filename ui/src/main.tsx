import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App.tsx";
import "./index.css";

// System-follow dark mode (ui.design.md principle 6). Manual override lands with the
// Settings/theme work; qn.0 just follows the OS.
function applyTheme(dark: boolean): void {
  document.documentElement.classList.toggle("dark", dark);
}
const mql = window.matchMedia("(prefers-color-scheme: dark)");
applyTheme(mql.matches);
mql.addEventListener("change", (e) => applyTheme(e.matches));

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
