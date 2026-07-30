// Typed client for the Admin BFF (§06). These endpoints are UI-shaped and NOT the public
// contract — that is the Model API (/v1). Shapes mirror the Go DTOs in adminui/bff.go.

export type Stage = "draft" | "staging" | "production" | "archived";

export interface AuditEvent {
  id: string;
  at: number;
  actor?: string;
  action: string;
  subjectType: string;
  subjectId: string;
  summary: string;
}

export interface Overview {
  counts: { models: number; versions: number; artifacts: number; deployments: number };
  stages: Record<Stage, number>;
  recent: AuditEvent[];
}

export interface ModelRollup {
  id: string;
  name: string;
  owner?: string;
  state: string;
  labels?: Record<string, string>;
  versionCount: number;
  production: string;
  updatedAt: number;
}

export interface VersionSummary {
  id: string;
  name: string;
  stage: Stage;
  author?: string;
  createdAt: number;
  updatedAt: number;
}

export interface ModelDetail {
  model: ModelRollup;
  versions: VersionSummary[];
  production: string;
}

export interface Artifact {
  id: string;
  name: string;
  kind: string;
  uri: string;
  digest?: string;
  sizeBytes?: number;
  mediaType?: string;
  storageBackend?: string;
  modelFormat?: { name: string; version?: string };
}

export interface LineageEdge {
  id: string;
  srcId: string;
  relation: string;
  dstId?: string;
  dstRef?: string;
  createdAt: number;
}

export interface Deployment {
  id: string;
  environment: string;
  endpointUri?: string;
  status: string;
  externalRef?: string;
  updatedAt: number;
}

export interface VersionDetail {
  model: string;
  version: VersionSummary & { description?: string; labels?: Record<string, string> };
  allowedTargets: Stage[];
  artifacts: Artifact[];
  lineage: LineageEdge[];
  deployments: Deployment[];
  audit: AuditEvent[];
}

async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(path, { headers: { Accept: "application/json" } });
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
  return r.json() as Promise<T>;
}

// Read a coded error (RFC 9457 problem+json) body for a friendly message.
async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    let detail = `${r.status} ${r.statusText}`;
    try {
      const p = await r.json();
      if (p.detail) detail = p.detail;
    } catch {
      /* ignore */
    }
    throw new Error(detail);
  }
  return r.json() as Promise<T>;
}

export const api = {
  overview: () => getJSON<Overview>("/api/overview"),
  models: (q = "") =>
    getJSON<{ items: ModelRollup[]; nextPageToken: string }>(`/api/models${q ? `?q=${encodeURIComponent(q)}` : ""}`),
  model: (m: string) => getJSON<ModelDetail>(`/api/models/${encodeURIComponent(m)}`),
  version: (m: string, v: string) =>
    getJSON<VersionDetail>(`/api/models/${encodeURIComponent(m)}/versions/${encodeURIComponent(v)}`),
  transition: (m: string, v: string, to: Stage, reason = "") =>
    postJSON<VersionSummary>(
      `/api/models/${encodeURIComponent(m)}/versions/${encodeURIComponent(v)}/transition`,
      { to, reason },
    ),
  activity: (token = "") =>
    getJSON<{ items: AuditEvent[]; nextPageToken: string }>(`/api/activity${token ? `?pageToken=${encodeURIComponent(token)}` : ""}`),
};
