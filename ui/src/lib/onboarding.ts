import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { OnboardingHTTPS } from "./types";

export const onboardingHTTPSKey = ["onboarding", "https"] as const;

// useOnboardingHTTPS reads GET /api/onboarding/https — whether this origin is already
// encrypted, so the check needs no action.
//
// NO RETRY. The endpoint is pre-auth and cannot 401, and its answer is a property of the
// connection this very request arrived on: if it failed, retrying over the same connection
// asks the same question and gets the same answer. Retrying would only delay the page.
export function useOnboardingHTTPS() {
  return useQuery({
    queryKey: onboardingHTTPSKey,
    queryFn: () => api.get<OnboardingHTTPS>("/api/onboarding/https"),
    retry: false,
  });
}
