import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { Config, ConfigResponse } from "./types";

export const configKey = ["config"] as const;

export function useConfig() {
  return useQuery({
    queryKey: configKey,
    queryFn: () => api.get<ConfigResponse>("/api/config"),
  });
}

// updateConfig PUTs the full document (contracts §1: full-document replace). A 422 surfaces
// as an APIError whose details carry {errors:[{path,message}]} for inline field errors.
export function updateConfig(config: Config): Promise<ConfigResponse> {
  return api.put<ConfigResponse>("/api/config", config);
}

// forgetStorage removes ONE storage from the declaration (contracts §1, qn.6d gap B).
//
// A NARROW ENDPOINT RATHER THAN updateConfig ABOVE, and the difference is not stylistic: a
// full-document PUT rebuilt from a list this client rendered drops every surviving entry's
// `retention:`. That key is a POINTER server-side, so an omitted one is indistinguishable from
// "keep none" and the code defaults silently take over. No storage surface renders retention, so
// this client could not round-trip it even in principle. The server splices instead.
//
// The name is percent-encoded because it comes from config and may contain a slash: `name`
// defaults to the PATH (quince#504), so on a single-storage install it usually IS one.
export function forgetStorage(name: string): Promise<ConfigResponse> {
  return api.del<ConfigResponse>(`/api/config/storage/${encodeURIComponent(name)}`);
}
