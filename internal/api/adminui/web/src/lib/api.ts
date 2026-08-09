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

// Risk classification (§16). The class is always something a person declared; the console
// shows it and shows when it has gone out of date, and never edits or infers one.
export type Regime = "eu_ai_act";

export type EUSystemRiskClass =
  | "unclassified"
  | "minimal"
  | "limited"
  | "high_annex_iii"
  | "high_annex_i"
  | "prohibited";

export type EUGpaiTier = "none" | "gpai" | "gpai_systemic";

export type ClassificationState = "unclassified" | "stale" | "current";

export type StaleReason =
  | "review_due_passed"
  | "version_published_since"
  | "production_changed_since"
  | "derivation_since";

export interface Classification {
  modelId: string;
  regime: Regime;
  euGpaiTier?: EUGpaiTier;
  euSystemRiskClass?: EUSystemRiskClass;
  intendedPurpose?: string;
  basis?: string;
  classifiedAt: number;
  classifiedBy?: string;
  reviewDueAt?: number;
  source: FactSource;
  state: ClassificationState;
  staleReasons?: StaleReason[];
}

// Legal hold (§19.3). A null hold is "not held" — there is no boolean beside the timestamp,
// because two encodings of one fact eventually disagree.
export interface Hold {
  heldSince: number;
  heldBy?: string;
}

// Evidence integrity (§19.8) — a property of the install, not of any model.
export interface RetentionConfig {
  minAuditAgeDays: number;
  minArchivedVersionDays: number;
}

export interface AttestationConfig {
  enabled: boolean;
  sealIntervalSeconds: number;
  sealGraceSeconds: number;
}

export type BreakKind = "root_mismatch" | "leaf_count_mismatch" | "prev_root_mismatch";

export interface VerifyBreak {
  epoch: number;
  kind: BreakKind;
  expected?: unknown;
  found?: unknown;
  sealedAt?: number;
}

export interface VerifyResult {
  ok: boolean;
  attestationStartedAt: number;
  epochsChecked: number;
  leavesChecked: number;
  // The window still accepting writes. Rows in it are not yet protected — reported, never
  // glossed (§19.5.3).
  openEpochSince: number;
  firstBreak: VerifyBreak | null;
}

export interface Evidence {
  retention: RetentionConfig;
  attestation: AttestationConfig;
  verify?: VerifyResult;
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
  // null when nobody has classified this model. Absence is the `unclassified` state (§16.4)
  // and must render as that word, never as `minimal` (§16.9).
  classification: Classification | null;
  // null when not held. A hold blocks destruction only — the model keeps moving through its
  // lifecycle (§19.3.1).
  legalHold: Hold | null;
}

export interface VersionSummary {
  id: string;
  name: string;
  stage: Stage;
  author?: string;
  createdAt: number;
  updatedAt: number;
  // This version's *own* hold. A version under a held model is not marked here — see
  // VersionDetail.modelHold.
  legalHold: Hold | null;
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

// Modification review (§17) — the Art. 25 queue. It surfaces derivations whose technical
// delta may have transferred provider liability, and records what a human concluded. It
// flags; it never decides whether a change is substantial in law, and it never blocks.
export type ReviewOutcome = "not_substantial" | "substantial" | "undetermined";

export type ReviewStatus = "open" | "closed";

export interface ModificationReview {
  id: string;
  versionId: string;
  edgeId: string;
  // The verdict as it stood when the review was recorded. Frozen — a producer submitting a
  // weights hash afterwards cannot rewrite what a reviewer saw.
  verdictAtReview: Verdict;
  outcome: ReviewOutcome;
  note?: string;
  reviewedBy?: string;
  reviewedAt: number;
}

export interface ReviewItem {
  model: string;
  version: string;
  versionId: string;
  edgeId: string;
  // One of the two is set: a version in this registry, or the external reference the edge
  // points at. The second is the Art. 25 case — somebody fine-tuned a third-party model.
  derivedFrom?: { model: string; version: string };
  derivedFromRef?: string;
  verdict: Verdict;
  candidates?: Verdict[];
  missing?: string[];
  hashes: Record<string, HashCmp>;
  basis: { fromHashes?: string[]; toHashes?: string[] };
  // The modifier's stated intent, from the edge's properties.method. Shown beside the
  // measurement so a reviewer can notice when the two disagree.
  declaredMethod?: string;
  euSystemRiskClass?: EUSystemRiskClass;
  euGpaiTier?: EUGpaiTier;
  edgeCreatedAt: number;
  status: ReviewStatus;
  // Present on a closed item. When review.verdictAtReview differs from verdict above, a
  // producer submitted a hash after the review — worth seeing, so both are rendered.
  review?: ModificationReview;
}

export interface ReviewInput {
  edgeId: string;
  outcome: ReviewOutcome;
  note: string;
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
  // The *model's* classification, shown on the version page because this is where someone
  // asks whether the thing they are about to promote is governed (§16.9).
  classification: Classification | null;
  // The owning model's hold, which covers this version transitively (§19.3.1). Separate from
  // version.legalHold so the page can say which subject is actually held — releasing the
  // wrong one is the mistake this prevents.
  modelHold: Hold | null;
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

async function putJSON<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(path, {
    method: "PUT",
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

// What a person can set. classifiedAt / classifiedBy / source are the server's and are
// rejected if sent, so they are absent here too.
export interface ClassificationInput {
  euSystemRiskClass: EUSystemRiskClass;
  euGpaiTier: EUGpaiTier;
  intendedPurpose: string;
  basis: string;
  reviewDueAt: number | null;
}

export const api = {
  overview: () => getJSON<Overview>("/api/overview"),
  models: (q = "", filters: Record<string, string> = {}) => {
    const p = new URLSearchParams();
    if (q) p.set("q", q);
    for (const [k, v] of Object.entries(filters)) if (v) p.set(k, v);
    const qs = p.toString();
    return getJSON<{ items: ModelRollup[]; nextPageToken: string }>(`/api/models${qs ? `?${qs}` : ""}`);
  },
  model: (m: string) => getJSON<ModelDetail>(`/api/models/${encodeURIComponent(m)}`),
  version: (m: string, v: string) =>
    getJSON<VersionDetail>(`/api/models/${encodeURIComponent(m)}/versions/${encodeURIComponent(v)}`),
  transition: (m: string, v: string, to: Stage, reason = "") =>
    postJSON<VersionSummary>(
      `/api/models/${encodeURIComponent(m)}/versions/${encodeURIComponent(v)}/transition`,
      { to, reason },
    ),
  setClassification: (m: string, input: ClassificationInput) =>
    putJSON<Classification>(
      `/api/models/${encodeURIComponent(m)}/classifications/eu_ai_act`,
      input,
    ),
  graph: (m: string, v: string, direction: "upstream" | "downstream" | "both") =>
    getJSON<LineageGraph>(
      `/api/models/${encodeURIComponent(m)}/versions/${encodeURIComponent(v)}/graph?direction=${direction}`,
    ),
  compare: (m: string, from: string, to: string) =>
    getJSON<InsightDiff>(
      `/api/models/${encodeURIComponent(m)}/compare?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  // Place or lift a legal hold. One shape for both subjects: omit `version` for a model.
  setHold: (subject: { model: string; version?: string }, hold: boolean, reason: string) => {
    const base = `/api/models/${encodeURIComponent(subject.model)}`;
    const path = subject.version
      ? `${base}/versions/${encodeURIComponent(subject.version)}`
      : base;
    return postJSON<{ legalHold: Hold | null }>(`${path}/${hold ? "hold" : "release"}`, { reason });
  },
  // The Art. 25 queue. No status default: "nothing to review" and "everything reviewed" are
  // different answers, and the page shows which one it is.
  reviews: (status: ReviewStatus | "" = "") =>
    getJSON<{ items: ReviewItem[]; nextPageToken: string }>(
      `/api/reviews${status ? `?status=${status}` : ""}`,
    ),
  recordReview: (m: string, v: string, input: ReviewInput) =>
    postJSON<ModificationReview>(
      `/api/models/${encodeURIComponent(m)}/versions/${encodeURIComponent(v)}/reviews`,
      input,
    ),
  evidence: () => getJSON<Evidence>("/api/evidence"),
  verifyEvidence: () => postJSON<Evidence>("/api/evidence:verify", {}),
  activity: (token = "") =>
    getJSON<{ items: AuditEvent[]; nextPageToken: string }>(`/api/activity${token ? `?pageToken=${encodeURIComponent(token)}` : ""}`),
};
