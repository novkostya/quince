export interface Health {
  status: string;
  version: string;
}

/** Fetch GET /api/health from the core daemon. */
export async function fetchHealth(signal?: AbortSignal): Promise<Health> {
  const res = await fetch("/api/health", { signal });
  if (!res.ok) {
    throw new Error(`health check failed: HTTP ${res.status}`);
  }
  return (await res.json()) as Health;
}
