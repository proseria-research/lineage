// "What needs attention" — one list assembled from every governance area, so the home page
// can answer the question without the reader visiting four queues. Nothing here is stored or
// enforced; it is a reading of the same facts each area's own page shows.

import type { ConformanceItem, ModelRollup, ReviewItem, StaleReason } from "@/lib/api";

export type Severity = "danger" | "warn";

export type Area = "eu" | "review" | "mrm" | "plan";

export const AREA_LABEL: Record<Area, string> = {
  eu: "EU AI Act",
  review: "Modification review",
  mrm: "Model risk",
  plan: "Change control",
};

export interface AttentionItem {
  key: string;
  severity: Severity;
  area: Area;
  model: string;
  version?: string;
  title: string;
  detail: string;
  /** Where to go to deal with it. */
  to: string;
  action: string;
}

export const REASON_TEXT: Record<StaleReason, string> = {
  review_due_passed: "Its scheduled review date has passed.",
  version_published_since: "A new version was published after it was assessed.",
  production_changed_since: "What's in production changed after it was assessed — a version was promoted to production.",
  derivation_since: "Someone derived a new model from it after it was assessed.",
  validation_expired: "Its validation has expired.",
  unmonitored_in_production: "It is in production with no evaluation recorded since it was promoted.",
  conditions_outstanding: "Its validation was conditional and the conditions haven't been cleared.",
};

const reasons = (rs?: StaleReason[]) => (rs ?? []).map((r) => REASON_TEXT[r] ?? r).join(" ");

const versionPath = (m: string, v?: string) =>
  v ? `/models/${encodeURIComponent(m)}/versions/${encodeURIComponent(v)}` : `/models/${encodeURIComponent(m)}`;

export function attentionItems(
  models: ModelRollup[],
  reviews: ReviewItem[],
  conformance: ConformanceItem[],
): AttentionItem[] {
  const out: AttentionItem[] = [];

  for (const m of models) {
    const c = m.classification;
    if (c?.euSystemRiskClass === "prohibited") {
      out.push({
        key: `eu-prohibited-${m.id}`,
        severity: "danger",
        area: "eu",
        model: m.name,
        title: "Classified as a prohibited AI practice",
        detail: "Your team recorded this model as prohibited under the EU AI Act.",
        to: versionPath(m.name),
        action: "Open model",
      });
    }
    if (c?.state === "stale") {
      out.push({
        key: `eu-stale-${m.id}`,
        severity: "warn",
        area: "eu",
        model: m.name,
        title: "Risk classification may be out of date",
        detail: reasons(c.staleReasons),
        to: "/compliance?tab=eu",
        action: "Re-classify",
      });
    }

    const r = m.mrm;
    if (r && r.mrmTier && r.mrmTier !== "untiered" && r.mrmTier !== "out_of_scope") {
      const tier1 = r.mrmTier === "tier_1";
      if (r.state === "stale") {
        out.push({
          key: `mrm-stale-${m.id}`,
          severity: tier1 ? "danger" : "warn",
          area: "mrm",
          model: m.name,
          version: r.version,
          title: tier1 ? "Tier 1 model needs attention" : "Validation is no longer current",
          detail: reasons(r.staleReasons),
          to: versionPath(m.name, r.version) + "?tab=governance",
          action: "Open version",
        });
      } else if (r.state === "unvalidated") {
        out.push({
          key: `mrm-unvalidated-${m.id}`,
          severity: tier1 ? "danger" : "warn",
          area: "mrm",
          model: m.name,
          version: r.version,
          title: "No accepted validation",
          detail: r.latestValidation
            ? `The latest validation concluded "${r.latestValidation.outcome}".`
            : "Nobody has validated the version in use yet.",
          to: versionPath(m.name, r.version) + "?tab=governance",
          action: "Open version",
        });
      }
    }
  }

  for (const it of reviews) {
    out.push({
      key: `review-${it.versionId}-${it.edgeId}`,
      severity: "warn",
      area: "review",
      model: it.model,
      version: it.version,
      title: "Modification waiting for review",
      detail: it.derivedFrom
        ? `Derived from ${it.derivedFrom.model} ${it.derivedFrom.version}, a high-risk model. Someone needs to judge whether the change was substantial.`
        : `Derived from ${it.derivedFromRef ?? "an external model"}. Someone needs to judge whether the change was substantial.`,
      to: "/compliance?tab=reviews",
      action: "Review",
    });
  }

  for (const it of conformance) {
    if (it.conformance === "outside_plan") {
      out.push({
        key: `plan-out-${it.versionId}-${it.edgeId}`,
        severity: "danger",
        area: "plan",
        model: it.model,
        version: it.version,
        title: "Shipped a change its plan doesn't allow",
        detail: `Measured as "${it.verdict}"; the plan in force allows ${
          (it.allowedVerdicts ?? []).map((v) => `"${v}"`).join(", ") || "no changes"
        }.`,
        to: "/compliance?tab=plans",
        action: "See why",
      });
    } else if (it.conformance === "undetermined") {
      out.push({
        key: `plan-und-${it.versionId}-${it.edgeId}`,
        severity: "warn",
        area: "plan",
        model: it.model,
        version: it.version,
        title: "Can't tell if this change is within plan",
        detail: `Missing ${(it.missing ?? ["some hashes"]).join(", ")}, so the kind of change can't be measured.`,
        to: "/compliance?tab=plans",
        action: "See why",
      });
    }
  }

  const rank = { danger: 0, warn: 1 };
  return out.sort((a, b) => rank[a.severity] - rank[b.severity] || a.model.localeCompare(b.model));
}
