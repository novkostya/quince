import { QueryClient } from "@tanstack/react-query";

// One client for the request/response resources (auth status, config). Devices/jobs/
// versions live in the WS-fed zustand stores, not here.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: false,
      refetchOnWindowFocus: false,
      staleTime: 5_000,
    },
  },
});
