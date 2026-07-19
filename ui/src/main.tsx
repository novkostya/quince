import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router-dom";
import { queryClient } from "@/lib/queryClient";
import { router } from "@/routes/router";
import { initTheme } from "@/lib/theme";
import "./index.css";

// System-follow theme at boot (ui.design.md principle 6); the Settings editor can override
// via config.ui.theme once loaded.
initTheme("system");

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
