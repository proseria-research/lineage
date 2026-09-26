import { useState } from "react";
import { Link } from "react-router-dom";
import { api, type ChangePlan, type Conformance, type ConformanceItem } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Badge } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import { BlinkMark, FingerprintMark, type RingName } from "@/components/VersionMark";
import { VerdictBadge, VERDICT_LABEL } from "@/components/Review";
import { relTime } from "@/lib/utils";

// Change control plans (§22). A plan is an envelope of pre-authorised changes, written in the
// verdict vocabulary; each derivation is judged against the plan in force when it shipped.
//
// Three things this view will not do:
//
//  - It will not say a new submission is required. `outside_plan` is a fact about the plan,
//    not a regulatory finding (§22.5).
//  - It will not block anything. There is no action here that stops a publish or a promotion.
//  - It will not pass an `undetermined`. A missing hash is queued beside `outside_plan`, not
//    filed with the conformant rows (§22.4.1).

const HASH_LEVELS = ["topology", "shape", "dtype", "weights"] as const;

// Weight, not colour: outside is filled, undetermined dashed (an absence of an answer),
// within outlined, uncovered muted — history, not a finding.
const CONFORMANCE_VARIANT: Record<Conformance, "ok" | "warn" | "danger" | "neutral" | "dashed"> = {
  outside_plan: "danger",
  undetermined: "warn",
  within_plan: "ok",
  uncovered: "neutral",
  no_plan: "dashed",
};

export const CONFORMANCE_LABEL: Record<Conformance, string> = {
  outside_plan: "Outside plan",
  undetermined: "Can't tell",
  within_plan: "Within plan",
  uncovered: "Before any plan",
  no_plan: "No plan",
};

const CONFORMANCE_TEXT: Record<Conformance, string> = {
  outside_plan: "The change is outside what the plan in force declared. Whether that needs a new submission is not decided here.",
  undetermined: "The hashes do not settle the verdict, so nobody can say whether it is inside the plan.",
  within_plan: "The verdict and the declared method are both inside the plan in force.",
  uncovered: "No plan was in force when this version was published. Not a violation.",
  no_plan: "The model has no change control plan.",
};

const fmtDate = (ms: number) => new Date(ms).toISOString().slice(0, 10);

export function ConformanceBadge({ c }: { c: Conformance }) {
  return (
    <Tooltip content={CONFORMANCE_TEXT[c]}>
      <Badge variant={CONFORMANCE_VARIANT[c]}>{CONFORMANCE_LABEL[c]}</Badge>
    </Tooltip>
  );
}

/** One derivation against its plan: the delta, the verdict, and what the plan allowed. */
function ConformanceRow({ it }: { it: ConformanceItem }) {
  const side = (which: "from" | "to") => ({
    hashes: Object.fromEntries(HASH_LEVELS.map((l) => [l, it.hashes?.[l]?.[which]])),
  });
  const changed = Object.fromEntries(
    HASH_LEVELS.map((l) => [l, it.hashes?.[l]?.changed === true]),
  ) as Partial<Record<RingName, boolean>>;
  const anyChanged = HASH_LEVELS.some((l) => changed[l]);
  const anyHash = HASH_LEVELS.some((l) => it.hashes?.[l]?.from || it.hashes?.[l]?.to);
  const parent = it.derivedFrom ? `${it.derivedFrom.model} ${it.derivedFrom.version}` : it.derivedFromRef;

  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-3 border-b px-4 py-3 last:border-b-0">
      {anyHash ? (
        <div className="fp-pair flex shrink-0 items-center gap-2">
          <FingerprintMark placeholder insight={side("from")} size={52} emphasis={anyChanged ? changed : undefined} />
          <span className="text-xs text-muted-foreground" aria-hidden="true">
            →
          </span>
          <BlinkMark from={side("from")} to={side("to")} size={52} emphasis={anyChanged ? changed : undefined} />
        </div>
      ) : (
        <div className="flex h-11 w-[7.25rem] shrink-0 items-center justify-center border border-dashed text-xs text-muted-foreground">
          no hashes
        </div>
      )}

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <Link
            to={`/models/${it.model}/versions/${it.version}`}
            className="text-sm font-medium hover:underline underline-offset-4"
          >
            {it.model} <span className="font-mono text-[0.8125rem] font-normal">{it.version}</span>
          </Link>
          <ConformanceBadge c={it.conformance} />
          <VerdictBadge v={it.verdict} />

        </div>
        {it.plan && (
          <div className="mt-1 text-sm text-muted-foreground">
            Plan {it.plan.ref || it.plan.id.slice(-6)} allows{" "}
            {(it.allowedVerdicts ?? []).map((v) => VERDICT_LABEL[v].toLowerCase()).join(" or ")} changes
            {it.allowedMethods?.length ? <> made by {it.allowedMethods.join(" or ")}</> : null}.
            {it.reasons?.length ? (
              <span className="text-danger">
                {" "}
                This version was {VERDICT_LABEL[it.verdict].toLowerCase()}
                {it.declaredMethod ? ` by ${it.declaredMethod}` : ""}.
              </span>
            ) : null}
          </div>
        )}
        <div className="mt-0.5 text-xs text-muted-foreground">
          Derived from {parent}, published {relTime(it.publishedAt)}
          {it.missing?.length ? <>. Missing {it.missing.join(", ")} hash.</> : null}
        </div>
      </div>
    </div>
  );
}

/** The plan register: every plan, superseded ones kept and marked (§22.6.1). */
function PlanList({ plans }: { plans: ChangePlan[] }) {
  return (
    <div className="border bg-card rounded-lg">
      {plans.map((p) => {
        const closed = p.effectiveTo != null;
        return (
          <div key={p.id} className="border-b px-4 py-3 last:border-b-0">
            <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
              <Link to={`/models/${p.model}`} className="font-medium hover:underline underline-offset-4">
                {p.model}
              </Link>
              {p.ref && <span className="text-xs text-muted-foreground">{p.ref}</span>}
              {closed ? <Badge variant="dashed">Replaced</Badge> : <Badge variant="ok">In force</Badge>}
              <span className="text-xs text-muted-foreground">
                allows {p.allowedVerdicts.map((v) => VERDICT_LABEL[v].toLowerCase()).join(", ")}
                {p.allowedMethods?.length ? ` by ${p.allowedMethods.join(", ")}` : ""}
              </span>
              <span className="ml-auto text-xs text-muted-foreground">
                {fmtDate(p.effectiveFrom)} – {closed ? fmtDate(p.effectiveTo!) : "now"}
              </span>
            </div>
            <div className="mt-1 text-sm text-muted-foreground">{p.summary}</div>
          </div>
        );
      })}
    </div>
  );
}

/**
 * The change-control section of the compliance workspace. It renders nothing when no model has
 * a plan — the regime is not in use on this install.
 */
export function ChangePlanSection() {
  const { data, error, loading } = useAsync(() => api.changePlans(), []);
  const [showRest, setShowRest] = useState(false);
  const [showPlans, setShowPlans] = useState(false);

  if (loading || error || !data || data.plans.length === 0) return null;
  const attention = data.items.filter((i) => i.conformance === "outside_plan" || i.conformance === "undetermined");
  const rest = data.items.filter((i) => !attention.includes(i));
  const open = data.plans.filter((p) => p.effectiveTo == null).length;

  return (
    <section className="mt-6">
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-semibold">Change control · what shipped against its plan</h2>
        <span className="text-xs text-muted-foreground">
          {open} plan{open === 1 ? "" : "s"} in force · {attention.length} need a look. Reported, never enforced.
        </span>
      </div>
      <div className="border bg-card rounded-lg">
        {attention.length === 0 ? (
          <div className="px-4 py-8 text-center text-sm text-muted-foreground">
            Every derivation under a plan is within it.
          </div>
        ) : (
          attention.map((it) => <ConformanceRow key={it.edgeId} it={it} />)
        )}
      </div>

      {rest.length > 0 && (
        <div className="mt-3">
          <button
            onClick={() => setShowRest((v) => !v)}
            className="flex w-full items-center justify-between border bg-card px-4 py-2.5 text-left text-sm hover:bg-accent rounded-lg"
          >
            <span className="font-semibold">Within plan or uncovered</span>
            <span className="text-xs text-muted-foreground">{showRest ? "Hide" : `${rest.length} · show`}</span>
          </button>
          {showRest && (
            <div className="mt-3 border bg-card rounded-lg">
              {rest.map((it) => (
                <ConformanceRow key={it.edgeId} it={it} />
              ))}
            </div>
          )}
        </div>
      )}

      <div className="mt-3">
        <button
          onClick={() => setShowPlans((v) => !v)}
          className="flex w-full items-center justify-between border bg-card px-4 py-2.5 text-left text-sm hover:bg-accent rounded-lg"
        >
          <span className="font-semibold">Plans</span>
          <span className="text-xs text-muted-foreground">
            {showPlans ? "Hide" : `${data.plans.length} · show`}
          </span>
        </button>
        {showPlans && (
          <div className="mt-3">
            <PlanList plans={data.plans} />
          </div>
        )}
      </div>
    </section>
  );
}
