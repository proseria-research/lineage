// Plain-language names for everything the API spells as a code. One place, so the same thing
// is called the same name on every page.

import type { Stage } from "@/lib/api";

/** Audit actions, as a short past-tense phrase: "Promoted", "Published a version". */
export const ACTION_LABEL: Record<string, string> = {
  "model.create": "Registered model",
  "model.update": "Updated model",
  "model.delete": "Deleted model",
  "version.create": "Published version",
  "version.update": "Updated version",
  "version.delete": "Deleted version",
  "version.stage_changed": "Changed stage",
  "artifact.register": "Registered artifact",
  "artifact.upload": "Uploaded artifact",
  "artifact.update": "Updated artifact",
  "artifact.delete": "Deleted artifact",
  "lineage.add": "Linked lineage",
  "lineage.delete": "Removed lineage link",
  "deployment.create": "Recorded deployment",
  "deployment.update": "Updated deployment",
  "deployment.delete": "Removed deployment",
  "insight.update": "Reported model facts",
  "insight.replace": "Replaced model facts",
  "evaluation.create": "Recorded evaluation",
  "footprint.update": "Reported footprint",
  "classification.set": "Set risk classification",
  "hold.set": "Placed legal hold",
  "hold.release": "Released legal hold",
  "review.record": "Recorded modification review",
  "validation.record": "Recorded validation",
  "validation.conditions_cleared": "Cleared validation conditions",
  "change_plan.declare": "Declared change plan",
  "change_plan.supersede": "Replaced change plan",
};

export const actionLabel = (a: string) => ACTION_LABEL[a] ?? humanize(a.replace(".", " "));

/** Which area an action belongs to, for the activity filter and its icon. */
export type ActionArea = "lifecycle" | "artifacts" | "lineage" | "facts" | "governance";

export function actionArea(a: string): ActionArea {
  const kind = a.split(".")[0];
  if (kind === "model" || kind === "version" || kind === "deployment") return "lifecycle";
  if (kind === "artifact") return "artifacts";
  if (kind === "lineage") return "lineage";
  if (kind === "insight" || kind === "evaluation" || kind === "footprint") return "facts";
  return "governance";
}

export const STAGE_LABEL: Record<Stage, string> = {
  draft: "Draft",
  staging: "Staging",
  production: "Production",
  archived: "Archived",
};

export const STAGE_HELP: Record<Stage, string> = {
  draft: "Published but not yet being tested",
  staging: "Being tested before release",
  production: "What consumers get when they ask for this model. At most one version at a time.",
  archived: "Retired; kept for the record",
};

/** snake_case or dotted codes to "Sentence case words". */
export function humanize(code: string): string {
  const s = code.replace(/[_.]+/g, " ").trim();
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** Short explanations for the terms the console cannot avoid. Shown as tooltips. */
export const GLOSSARY = {
  euRiskClass:
    "The risk category your team assigned under the EU AI Act. Lineage records it; it never decides it.",
  gpai: "Whether the model is a general-purpose AI model under the EU AI Act, and if so whether it carries systemic risk.",
  staleClassification:
    "Something changed after the model was classified — a new version, a new production version, or the review date passed — so the classification may no longer be accurate.",
  modificationReview:
    "When someone fine-tunes or otherwise changes a high-risk model, the EU AI Act may make them its provider. These are derivations waiting for a person to judge whether the change was substantial.",
  modelRisk:
    "Supervisory model risk management (SR 26-2, PRA SS1/23, OSFI E-23): each model gets a risk tier, and versions need an independent validation and ongoing monitoring.",
  tier: "How much damage the model could do if it went wrong. Tier 1 gets the most scrutiny.",
  changePlan:
    "A declared list of the kinds of change a model may make without a fresh review (FDA PCCP style). Lineage checks every new version against the plan in force when it shipped.",
  fingerprint:
    "Four hashes, from the coarsest (architecture) to the finest (the exact weights). Comparing them tells you what kind of change a new version is.",
  legalHold:
    "A hold stops the model or version from being deleted while a legal matter is open. Everything else still works.",
  auditIntegrity:
    "The audit log is sealed in batches with a hash chain, so any edit or deletion after the fact can be detected.",
} as const;
