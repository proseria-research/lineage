import { useState } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ClassifyDialog } from "@/components/ClassifyDialog";
import type { Classification, ClassificationState, EUSystemRiskClass, MRMState, StaleReason } from "@/lib/api";

// Risk-classification rendering (§16.9). Three rules run through everything here:
//
//  1. `unclassified` shows as the word `unclassified`. It never renders as `minimal`, and a
//     model with no row renders the same as one that says `unclassified` — both mean nobody
//     has said yet (§16.3).
//  2. A "stale" badge is never a dead end. Where there is room — the compliance worklist,
//     the version panel — the reason is spelled out beside it. Where there is not (a table
//     cell), the badge links to the worklist, which is where the reason and the fix live.
//     What §16.9 rules out is marking something stale and leaving the reader to hunt.
//  3. Nothing here offers to fix anything. There is no "mark as current" — staleness clears
//     only by writing a real new classification (§16.9).

const CLASS_LABEL: Record<EUSystemRiskClass, string> = {
  unclassified: "unclassified",
  minimal: "minimal",
  limited: "limited",
  high_annex_iii: "high · Annex III",
  high_annex_i: "high · Annex I",
  prohibited: "prohibited",
};

// High risk and prohibited are the classes a reader must not skim past, so they take the
// filled badge. Weight, not colour — the console is monochrome by design (§06).
const CLASS_VARIANT: Record<EUSystemRiskClass, "solid" | "outline" | "muted" | "dashed"> = {
  unclassified: "dashed",
  minimal: "muted",
  limited: "muted",
  high_annex_iii: "solid",
  high_annex_i: "solid",
  prohibited: "solid",
};

export const STALE_REASON_TEXT: Record<StaleReason, string> = {
  review_due_passed: "the review date has passed",
  version_published_since: "a version was published since it was classified",
  production_changed_since: "the production version changed since it was classified",
  derivation_since: "a modification review was opened since it was classified",
  // The model-risk clauses (§20.7). Their own panel phrases version_published_since against
  // the validation rather than the classification.
  validation_expired: "the validation has expired",
  unmonitored_in_production: "in production with no evaluation since it was promoted",
  conditions_outstanding: "the validation's conditions have not been cleared",
};

/** A badge for a bare class value, for headings and legends that have no row behind them. */
export function ClassBadge({ cls }: { cls: EUSystemRiskClass }) {
  return <Badge variant={CLASS_VARIANT[cls]}>{CLASS_LABEL[cls]}</Badge>;
}

/** The declared class, or an explicit `unclassified` when there is no row. */
export function RiskClassBadge({ c }: { c: Classification | null }) {
  return <ClassBadge cls={c?.euSystemRiskClass ?? "unclassified"} />;
}

/**
 * The staleness marker. Renders nothing for `current` — a page full of "ok" badges buries
 * the two rows that matter.
 */
export function StaleBadge({ state }: { state: ClassificationState | MRMState }) {
  if (state !== "stale") return null;
  return <Badge variant="outline">stale</Badge>;
}

/**
 * One table cell: the class, and a staleness marker that leads somewhere.
 *
 * The reason text lives on the compliance worklist rather than here — in a table it wrapped
 * to a second line and broke the row rhythm for a detail most readers scanning the model
 * list are not acting on. The badge stays clickable so the reader is one step from the
 * reason and the fix.
 */
export function ClassificationCell({ c }: { c: Classification | null }) {
  return (
    <div className="flex items-center gap-1.5">
      <RiskClassBadge c={c} />
      {c?.state === "stale" && (
        <Link to="/compliance" title="Why this is stale" className="hover:opacity-70">
          <StaleBadge state={c.state} />
        </Link>
      )}
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="text-[0.6875rem] uppercase tracking-wider text-muted-foreground">{label}</div>
      <div>{children}</div>
    </div>
  );
}

const fmt = (ms?: number) => (ms ? new Date(ms).toISOString().slice(0, 10) : "—");

/**
 * The Compliance panel (§16.9). Shown on the version page, where the question is whether
 * the model being promoted is governed and whether that assessment still holds.
 */
export function CompliancePanel({
  c,
  model,
  onSaved,
}: {
  c: Classification | null;
  // When given, the panel can be acted on where it is read — a reader who notices a stale
  // classification on the page they are about to promote from should not have to go
  // elsewhere to fix it.
  model?: string;
  onSaved?: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const action = model ? (
    <Button size="sm" variant={c ? "outline" : "default"} onClick={() => setEditing(true)}>
      {c ? "Re-classify" : "Classify"}
    </Button>
  ) : null;
  const dialog =
    model && editing ? (
      <ClassifyDialog
        model={model}
        current={c}
        onClose={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          onSaved?.();
        }}
      />
    ) : null;

  if (!c) {
    return (
      <div className="border border-border p-4">
        <div className="mb-2 flex items-center justify-between gap-3">
          <div className="text-sm font-medium">Compliance · EU AI Act</div>
          {action}
        </div>
        <p className="text-sm text-muted-foreground">
          Not classified. Nobody has stated a risk class for this model — which is a real
          answer, not a low one.
        </p>
        {dialog}
      </div>
    );
  }

  return (
    <div className="border border-border p-4">
      {/* The section header is the regime: one section per regime once there is more than
          one (§16.9), and the reader should never have to guess whose rulebook this is. */}
      <div className="mb-3 flex items-center justify-between">
        <div className="text-sm font-medium">Compliance · EU AI Act</div>
        <div className="flex items-center gap-2">
          <RiskClassBadge c={c} />
          <StaleBadge state={c.state} />
          {action}
        </div>
      </div>

      <div className="grid gap-4 text-sm sm:grid-cols-2">
        <Field label="System risk class">{CLASS_LABEL[c.euSystemRiskClass ?? "unclassified"]}</Field>
        <Field label="GPAI tier">{c.euGpaiTier ?? "—"}</Field>
        <Field label="Intended purpose">
          {c.intendedPurpose || <span className="text-muted-foreground">not stated</span>}
        </Field>
        <Field label="Basis">
          {c.basis || <span className="text-muted-foreground">not stated</span>}
        </Field>
        <Field label="Classified">
          <span className="font-mono text-xs">{fmt(c.classifiedAt)}</span>
          {c.classifiedBy && <span className="text-muted-foreground"> by {c.classifiedBy}</span>}
        </Field>
        <Field label="Review due">
          {c.reviewDueAt ? (
            <span className="font-mono text-xs">{fmt(c.reviewDueAt)}</span>
          ) : (
            // Surfaced, not hidden: "no review scheduled" is something a reader should see.
            <span className="text-muted-foreground">no review scheduled</span>
          )}
        </Field>
      </div>

      {c.state === "stale" && (
        <div className="mt-4 border-t border-border pt-3 text-sm">
          <div className="mb-1 text-[0.6875rem] uppercase tracking-wider text-muted-foreground">
            Why this is stale
          </div>
          <ul className="list-disc space-y-0.5 pl-4 text-muted-foreground">
            {(c.staleReasons ?? []).map((r) => (
              <li key={r}>{STALE_REASON_TEXT[r] ?? r}</li>
            ))}
          </ul>
        </div>
      )}
      {dialog}
    </div>
  );
}
