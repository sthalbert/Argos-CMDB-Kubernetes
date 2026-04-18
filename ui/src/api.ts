// Thin fetch wrapper around the Argos REST API. Hand-written for v0 — a
// generated OpenAPI client can replace this when the surface grows.
//
// The bearer token is stored in sessionStorage (cleared on tab close),
// matching ADR-0006's auth choice. Callers read it via getToken() and
// pass it via request() which injects the Authorization header.

const TOKEN_KEY = 'argos.token';

export function getToken(): string | null {
  return sessionStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  sessionStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  sessionStorage.removeItem(TOKEN_KEY);
}

export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

async function request<T>(path: string): Promise<T> {
  const token = getToken();
  const res = await fetch(path, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!res.ok) {
    // RFC 7807 problem+json bodies carry a useful 'detail'. Fall back to status text.
    let detail = res.statusText;
    try {
      const body = await res.json();
      if (body && typeof body.detail === 'string') {
        detail = body.detail;
      } else if (body && typeof body.title === 'string') {
        detail = body.title;
      }
    } catch {
      // Non-JSON body — keep statusText.
    }
    throw new ApiError(res.status, detail);
  }
  return res.json() as Promise<T>;
}

// Subset of fields returned by /v1/clusters. Expanded as the UI grows.
export interface Cluster {
  id: string;
  name: string;
  display_name?: string | null;
  environment?: string | null;
  provider?: string | null;
  region?: string | null;
  kubernetes_version?: string | null;
  api_endpoint?: string | null;
  layer: string;
  created_at: string;
  updated_at: string;
}

export interface ClusterList {
  items: Cluster[];
  next_cursor?: string | null;
}

export function listClusters(): Promise<ClusterList> {
  return request<ClusterList>('/v1/clusters');
}

export interface Health {
  status: string;
  version?: string;
}

export function getHealthz(): Promise<Health> {
  return request<Health>('/healthz');
}
