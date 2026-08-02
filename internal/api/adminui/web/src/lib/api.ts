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
  // Action-specific payload recorded with the event; version.stage_changed carries from/to/reason.
  data?: { from?: Stage; to?: Stage; reason?: string };
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

export interface LineageNode {
  type: "model_version" | "external";
  id?: string;
  ref?: string;
  model?: string;
  version?: string;
  stage?: Stage;
  label: string;
  depth: number;
}

export interface LineageGraph {
  root: string;
  direction: string;
  nodes: LineageNode[];
  edges: LineageEdge[];
}

export interface Deployment {
  id: string;
  environment: string;
  endpointUri?: string;
  status: string;
  externalRef?: string;
  updatedAt: number;
}

// ---- Model insights (§11). Facts reported by producers; the registry derives none of
// them, so every value carries where it came from and absent means "not reported". ----

export type FactSource = "declared" | "derived" | "measured";

export interface FieldSource {
  source: FactSource;
  reporter?: string;
  reporterVersion?: string;
  at: number;
}

export interface LayerBlock {
  ordinal: number;
  path: string;
  opType?: string;
  repeatCount: number;
  shapeSignature?: string;
  dtype?: string;
  paramCount?: number | null;
  bytes?: number | null;
}

export interface VersionInsight {
  versionId: string;
  framework?: { name?: string; version?: string };
  producer?: { name?: string; version?: string };
  paramCountTotal?: number | null;
  paramCountTrainable?: number | null;
  paramCountMethod?: string;
  tensorCount?: number | null;
  dtypeDominant?: string;
  quantMethod?: string;
  diskBytes?: number | null;
  weightsBytes?: number | null;
  hashes: { topology?: string; shape?: string; dtype?: string; weights?: string };
  source: FactSource;
  fieldSources?: Record<string, FieldSource>;
  reporterName?: string;
  reporterVersion?: string;
  coverage?: Record<string, string>;
  layers?: LayerBlock[];
  updatedAt: number;
}

export interface Footprint {
  id: string;
  scenario: string;
  deviceClass?: string;
  batch?: number | null;
  seqLen?: number | null;
  weightsBytes?: number | null;
  kvCacheBytes?: number | null;
  activationBytes?: number | null;
  runtimeOverheadBytes?: number | null;
  totalBytes?: number | null;
  source: "estimated" | "measured";
  basis?: Record<string, unknown>;
}

export interface Evaluation {
  id: string;
  suite: string;
  metric: string;
  split?: string;
  value: number;
  higherIsBetter: boolean;
  nSamples?: number | null;
  harnessName?: string;
  harnessVersion?: string;
  source: FactSource;
  runAt?: number;
}

export type Verdict = "identical" | "reweighted" | "recast" | "rescaled" | "rearchitected" | "unknown";

export interface HashCmp {
  from?: string;
  to?: string;
  changed?: boolean | null;
  present: boolean;
}

export interface InsightDiff {
  from: { model: string; version: string };
  to: { model: string; version: string };
  verdict: Verdict;
  candidates?: Verdict[];
  missing?: string[];
  hashes: Record<string, HashCmp>;
  tensors?: { unchanged: number; changed: number; added: number; removed: number; changedPatterns?: string[] };
  params?: { field: string; from?: number | null; to?: number | null; delta?: number | null; fromSource?: string; toSource?: string }[];
  footprints?: {
    scenario: string;
    fromTotalBytes?: number | null;
    toTotalBytes?: number | null;
    delta?: number | null;
    fromSource?: string;
    toSource?: string;
    comparable: boolean;
  }[];
  metrics?: {
    suite: string;
    metric: string;
    split?: string;
    harnessVersion?: string;
    from?: number | null;
    to?: number | null;
    delta?: number | null;
    direction?: "better" | "worse" | "same";
    comparable: boolean;
    reason?: string;
  }[];
  basis: {
    fromHasInsight: boolean;
    toHasInsight: boolean;
    fromHashes?: string[];
    toHashes?: string[];
    tensorDigests: boolean;
  };
}

export interface VersionDetail {
  model: string;
  version: VersionSummary & { description?: string; labels?: Record<string, string> };
  allowedTargets: Stage[];
  artifacts: Artifact[];
  lineage: LineageEdge[];
  deployments: Deployment[];
  audit: AuditEvent[];
  // null when no producer has reported on this version.
  insight: VersionInsight | null;
  footprints: Footprint[];
  evaluations: Evaluation[];
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
  graph: (m: string, v: string, direction: "upstream" | "downstream" | "both") =>
    getJSON<LineageGraph>(
      `/api/models/${encodeURIComponent(m)}/versions/${encodeURIComponent(v)}/graph?direction=${direction}`,
    ),
  compare: (m: string, from: string, to: string) =>
    getJSON<InsightDiff>(
      `/api/models/${encodeURIComponent(m)}/compare?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  activity: (token = "") =>
    getJSON<{ items: AuditEvent[]; nextPageToken: string }>(`/api/activity${token ? `?pageToken=${encodeURIComponent(token)}` : ""}`),
};
