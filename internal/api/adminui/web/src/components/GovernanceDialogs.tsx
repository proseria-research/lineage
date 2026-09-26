import { useState } from "react";
import {
  api,
  type Artifact,
  type ChangePlan,
  type Classification,
  type MRMTier,
  type PlannableVerdict,
  type ValidationOutcome,
} from "@/lib/api";
import { Input } from "@/components/ui/input";
import {
  controlClass,
  Dialog,
  DialogFooter,
  Field,
  FormError,
  fromDateInput,
  toDateInput,
} from "@/components/ui/dialog";
import { VERDICT_TEXT, VERDICT_LABEL } from "@/components/Review";

// The console's model-risk and change-control forms. Each writes through the BFF to the same
// core operation /v1 exposes, so validation, attribution and the audit event are identical.
// Who recorded it and when come from the actor header and the server clock, never the form.

const useSave = (fn: () => Promise<unknown>, onSaved: () => void) => {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await fn();
      onSaved();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, save };
};

// ---- Model-risk tier (§20.4) ----

const TIERS: { value: MRMTier; label: string; hint: string }[] = [
  { value: "untiered", label: "No tier", hint: "Nobody has assessed it yet." },
  { value: "tier_1", label: "Tier 1", hint: "Highest materiality — the most scrutiny. Needs a stated basis." },
  { value: "tier_2", label: "Tier 2", hint: "Material, with standard validation." },
  { value: "tier_3", label: "Tier 3", hint: "Low materiality." },
  { value: "out_of_scope", label: "Out of scope", hint: "Not a model under the framework. Needs a stated basis." },
];

export function TierDialog({
  model,
  current,
  onClose,
  onSaved,
}: {
  model: string;
  current: Classification | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [tier, setTier] = useState<MRMTier>(current?.mrmTier ?? "untiered");
  const [basis, setBasis] = useState(current?.basis ?? "");
  const [purpose, setPurpose] = useState(current?.intendedPurpose ?? "");
  const [review, setReview] = useState(toDateInput(current?.reviewDueAt));
  const reviewMs = fromDateInput(review);

  const needsBasis = (tier === "tier_1" || tier === "out_of_scope") && !basis.trim();
  const reviewInPast = reviewMs !== null && reviewMs <= Date.now();
  const { busy, error, save } = useSave(
    () => api.setMRMTier(model, { mrmTier: tier, basis: basis.trim(), intendedPurpose: purpose.trim(), reviewDueAt: reviewMs }),
    onSaved,
  );

  return (
    <Dialog
      title={
        <>
          {current ? "Change model-risk tier" : "Set model-risk tier"} for <span className="font-mono">{model}</span>
        </>
      }
      sub="Recorded as your assessment and dated now. This doesn't affect the EU AI Act classification."
      onClose={onClose}
      footer={
        <DialogFooter busy={busy} blocked={needsBasis || reviewInPast} onCancel={onClose} onSave={save} saveLabel="Save tier" />
      }
    >
      <Field label="Tier" hint={TIERS.find((t) => t.value === tier)?.hint}>
        <select value={tier} onChange={(e) => setTier(e.target.value as MRMTier)} className={controlClass}>
          {TIERS.map((t) => (
            <option key={t.value} value={t.value}>
              {t.label}
            </option>
          ))}
        </select>
      </Field>
      <Field
        label="Basis"
        required={tier === "tier_1" || tier === "out_of_scope"}
        hint="Why this tier — the reasoning, not the conclusion."
        error={needsBasis ? "This tier has to record why." : undefined}
      >
        <textarea
          value={basis}
          onChange={(e) => setBasis(e.target.value)}
          rows={2}
          placeholder="Automated declines on the authorization path; direct customer impact above $500."
          className={controlClass}
        />
      </Field>
      <Field label="What the model is used for" hint="Optional.">
        <textarea value={purpose} onChange={(e) => setPurpose(e.target.value)} rows={2} className={controlClass} />
      </Field>
      <Field
        label="Next review"
        hint="Optional. Leave empty for no scheduled review."
        error={reviewInPast ? "The review date has to be in the future." : undefined}
      >
        <Input type="date" value={review} onChange={(e) => setReview(e.target.value)} className="max-w-[12rem]" />
      </Field>
      {current && (
        <p className="text-xs text-muted-foreground">
          This replaces the tier on file. The previous one stays in the audit log.
        </p>
      )}
      <FormError error={error} />
    </Dialog>
  );
}

// ---- Validation (§20.5) ----

const OUTCOMES: { value: ValidationOutcome; label: string; hint: string }[] = [
  { value: "approved", label: "Approved", hint: "Fit for its intended use." },
  { value: "conditional", label: "Approved with conditions", hint: "Fit, once the conditions are met. List them below." },
  { value: "rejected", label: "Rejected", hint: "Not fit for its intended use." },
  { value: "undetermined", label: "Undetermined", hint: "Couldn't reach a conclusion yet." },
];

export function ValidationDialog({
  model,
  version,
  artifacts,
  onClose,
  onSaved,
}: {
  model: string;
  version: string;
  artifacts: Artifact[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [outcome, setOutcome] = useState<ValidationOutcome>("approved");
  const [scope, setScope] = useState("");
  const [findings, setFindings] = useState("");
  const [conditions, setConditions] = useState("");
  const [until, setUntil] = useState("");
  const [evidence, setEvidence] = useState("");
  const untilMs = fromDateInput(until);
  const reports = artifacts.filter((a) => a.kind === "DOC" || a.kind === "METRICS");

  const needsConditions = outcome === "conditional" && !conditions.trim();
  const untilInPast = untilMs !== null && untilMs <= Date.now();
  const { busy, error, save } = useSave(
    () =>
      api.recordValidation(model, version, {
        outcome,
        scope: scope.trim(),
        findings: findings.trim(),
        conditions: outcome === "conditional" ? conditions.trim() : "",
        validUntil: untilMs,
        evidenceArtifactId: evidence,
      }),
    onSaved,
  );

  return (
    <Dialog
      title={
        <>
          Record a validation of <span className="font-mono">{model} {version}</span>
        </>
      }
      sub="Recorded under your name and dated now. If you wrote this version, it will be flagged as not independent — it is still recorded."
      onClose={onClose}
      footer={
        <DialogFooter
          busy={busy}
          blocked={needsConditions || untilInPast}
          onCancel={onClose}
          onSave={save}
          saveLabel="Record validation"
        />
      }
    >
      <Field label="Conclusion" hint={OUTCOMES.find((o) => o.value === outcome)?.hint}>
        <select value={outcome} onChange={(e) => setOutcome(e.target.value as ValidationOutcome)} className={controlClass}>
          {OUTCOMES.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </Field>
      {outcome === "conditional" && (
        <Field
          label="Conditions"
          required
          hint="What has to happen before the conditions can be marked cleared."
          error={needsConditions ? "List the conditions." : undefined}
        >
          <textarea
            value={conditions}
            onChange={(e) => setConditions(e.target.value)}
            rows={2}
            placeholder="Re-run the back-test on Q3 data before the next promotion."
            className={controlClass}
          />
        </Field>
      )}
      <Field label="Scope" hint="Optional. What was covered — conceptual soundness, back-testing, and so on.">
        <Input value={scope} onChange={(e) => setScope(e.target.value)} />
      </Field>
      <Field label="Findings" hint="Optional.">
        <textarea value={findings} onChange={(e) => setFindings(e.target.value)} rows={3} className={controlClass} />
      </Field>
      <Field
        label="Valid until"
        hint="Optional. After this date the validation counts as expired."
        error={untilInPast ? "Pick a date in the future." : undefined}
      >
        <Input type="date" value={until} onChange={(e) => setUntil(e.target.value)} className="max-w-[12rem]" />
      </Field>
      <Field
        label="Validation report"
        hint={reports.length ? "Optional. A document or metrics file already attached to this version." : "Optional. Attach a document or metrics file to this version to link it here."}
      >
        <select value={evidence} onChange={(e) => setEvidence(e.target.value)} className={controlClass} disabled={!reports.length}>
          <option value="">None</option>
          {reports.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
      </Field>
      <FormError error={error} />
    </Dialog>
  );
}

// ---- Change plan (§22.3) ----

const sentence = (t: string) => t.charAt(0).toUpperCase() + t.slice(1);

const PLANNABLE: PlannableVerdict[] = ["identical", "reweighted", "recast", "rescaled", "rearchitected"];

export function ChangePlanDialog({
  model,
  open,
  onClose,
  onSaved,
}: {
  model: string;
  /** The plan currently in force, which a new one replaces. */
  open: ChangePlan | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [ref, setRef] = useState("");
  const [summary, setSummary] = useState("");
  const [verdicts, setVerdicts] = useState<PlannableVerdict[]>(open?.allowedVerdicts ?? ["identical", "reweighted"]);
  const [methods, setMethods] = useState((open?.allowedMethods ?? []).join(", "));
  const [from, setFrom] = useState("");
  const fromMs = fromDateInput(from);

  const methodList = methods
    .split(",")
    .map((m) => m.trim())
    .filter(Boolean);
  const noSummary = !summary.trim();
  const noVerdicts = verdicts.length === 0;
  const tooEarly = open && fromMs !== null && fromMs <= open.effectiveFrom;

  const toggle = (v: PlannableVerdict) =>
    setVerdicts((cur) => (cur.includes(v) ? cur.filter((x) => x !== v) : [...cur, v]));

  const { busy, error, save } = useSave(
    () =>
      api.declareChangePlan(model, {
        ref: ref.trim(),
        summary: summary.trim(),
        allowedVerdicts: PLANNABLE.filter((v) => verdicts.includes(v)),
        allowedMethods: methodList.length ? methodList : undefined,
        effectiveFrom: fromMs ?? undefined,
        supersedes: open?.id,
      }),
    onSaved,
  );

  return (
    <Dialog
      title={
        <>
          {open ? "Replace the change plan" : "Declare a change plan"} for <span className="font-mono">{model}</span>
        </>
      }
      sub={
        open
          ? `Plan ${open.ref || "in force"} stays on record and stops applying when this one starts.`
          : "Every new version derived from this model will be checked against it. Nothing is ever blocked."
      }
      onClose={onClose}
      footer={
        <DialogFooter
          busy={busy}
          blocked={noSummary || noVerdicts || !!tooEarly}
          onCancel={onClose}
          onSave={save}
          saveLabel={open ? "Replace plan" : "Declare plan"}
        />
      }
    >
      <Field label="Reference" hint="Optional. Your document or submission number, e.g. PCCP-2026-01.">
        <Input value={ref} onChange={(e) => setRef(e.target.value)} />
      </Field>
      <Field label="Summary" required hint="What the plan permits, in your own words.">
        <textarea
          value={summary}
          onChange={(e) => setSummary(e.target.value)}
          rows={2}
          placeholder="Periodic retraining on new data; no architecture changes."
          className={controlClass}
        />
      </Field>
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1.5 text-sm font-medium">Changes the plan allows</legend>
        {PLANNABLE.map((v) => (
          <label key={v} className="flex items-start gap-2.5 rounded-md px-1 py-1 text-sm hover:bg-muted">
            <input type="checkbox" className="mt-1" checked={verdicts.includes(v)} onChange={() => toggle(v)} />
            <span>
              <span className="font-medium">{VERDICT_LABEL[v]}</span>
              <span className="block text-xs text-muted-foreground">{sentence(VERDICT_TEXT[v].replace(/^Measured: /, ""))}</span>
            </span>
          </label>
        ))}
        {noVerdicts && <span className="text-xs text-danger">Allow at least one kind of change.</span>}
      </fieldset>
      <Field label="Allowed methods" hint="Optional. Comma-separated, e.g. retrain, fine_tune. Leave empty to allow any method.">
        <Input value={methods} onChange={(e) => setMethods(e.target.value)} />
      </Field>
      <Field
        label="In force from"
        hint="Optional. Defaults to now."
        error={tooEarly ? "Must be after the current plan started." : undefined}
      >
        <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="max-w-[12rem]" />
      </Field>
      <FormError error={error} />
    </Dialog>
  );
}
