import { Link } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { STALE_REASON_TEXT } from "@/components/Classification";
import type { Classification, MRMState, MRMTier, StaleReason, Validation, VersionValidations } from "@/lib/api";
import { relTime } from "@/lib/utils";

// Model-risk rendering (§20.10). The same rules as the EU lens, plus two of its own:
//
//  1. `untiered` shows as the word `untiered`, never as `tier_3` — guessing low is the
//     expensive direction. A model with no row renders the same as one that says so.
//  2. Stale is shown with its reason. `unmonitored_in_production` is surfaced loudest: it is
//     a live gap, not a paperwork one.
//  3. Validation history is a timeline, not a current-value field. The supersession is the
//     point, so superseded rows stay and are marked, never dropped.
//  4. Nothing here gates or fixes anything. A self-validation is flagged, not refused.

const STATE_LABEL: Record<MRMState, string> = {
  untiered: "No tier",
  unvalidated: "Not validated",
  stale: "Out of date",
  current: "Current",
};

export const TIER_LABEL: Record<MRMTier, string> = {
  untiered: "No tier",
  tier_1: "Tier 1",
  tier_2: "Tier 2",
  tier_3: "Tier 3",
  out_of_scope: "Out of scope",
};

// Tier 1 takes the filled badge for the reason high risk does on the EU lens: weight, not
// colour. Untiered is dashed — an absence of an answer, visibly.
const TIER_VARIANT: Record<MRMTier, "brand" | "neutral" | "dashed"> = {
  untiered: "dashed",
  tier_1: "brand",
  tier_2: "neutral",
  tier_3: "neutral",
  out_of_scope: "dashed",
};

// §20.7 clause 2 measures against the validation, not the classification, so it is phrased
// here rather than borrowed from the EU text.
const reasonText = (r: StaleReason) =>
  r === "version_published_since" ? "a version was published since it was validated" : (STALE_REASON_TEXT[r] ?? r);

export function TierBadge({ tier }: { tier: MRMTier }) {
  return <Badge variant={TIER_VARIANT[tier]}>{TIER_LABEL[tier]}</Badge>;
}

/** The state marker. Nothing for `current` or `untiered` — the tier badge already says the latter. */
export function MRMStateBadge({ state }: { state: MRMState }) {
  if (state === "stale") return <Badge variant="warn">Out of date</Badge>;
  if (state === "unvalidated") return <Badge variant="warn">Not validated</Badge>;
  return null;
}

/**
 * One table cell: the tier, and a state marker that leads to the subject version's page,
 * where the reason and the validation history are. `unmonitored_in_production` is named
 * inline even here, because it is the one a reader scanning the table must not miss.
 */
export function MRMCell({ model, c }: { model: string; c: Classification | null }) {
  const state = (c?.state ?? "untiered") as MRMState;
  const unmonitored = c?.staleReasons?.includes("unmonitored_in_production");
  const marker = <MRMStateBadge state={state} />;
  return (
    <div className="flex items-center gap-1.5">
      <TierBadge tier={c?.mrmTier ?? "untiered"} />
      {c?.version && (state === "stale" || state === "unvalidated") ? (
        <Link to={`/models/${model}/versions/${c.version}`} title="Why" className="hover:opacity-70">
          {marker}
        </Link>
      ) : (
        marker
      )}
      {unmonitored && <Badge variant="danger">Unmonitored</Badge>}
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div>{children}</div>
    </div>
  );
}

const fmt = (ms?: number) => (ms ? new Date(ms).toISOString().slice(0, 10) : "—");

function Reasons({ state, reasons }: { state: MRMState; reasons?: StaleReason[] }) {
  if (state !== "stale") return null;
  return (
    <div className="mt-4 border-t border-border pt-3 text-sm">
      <div className="mb-1 text-xs text-muted-foreground">Why it needs attention</div>
      <ul className="list-disc space-y-0.5 pl-4 text-muted-foreground">
        {(reasons ?? []).map((r) => (
          <li key={r} className={r === "unmonitored_in_production" ? "font-medium text-foreground" : undefined}>
            {reasonText(r)}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** One validation row. The first is the current answer; the rest are history. */
function ValidationRow({ v, superseded }: { v: Validation; superseded: boolean }) {
  return (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b px-4 py-2.5 last:border-b-0">
      <Badge variant={superseded ? "dashed" : v.outcome === "rejected" ? "solid" : "outline"}>{v.outcome}</Badge>
      {superseded && <span className="label-caps">superseded</span>}
      {!v.independenceEvidenced && (
        // Flagged, not refused (§20.6): a one-person team trips this legitimately.
        <span className="label-caps" title="The validator is not named, or is the version's author">
          independence not evidenced
        </span>
      )}
      {v.validUntil && <span className="text-xs text-muted-foreground">valid until {fmt(v.validUntil)}</span>}
      <span className="ml-auto text-xs text-muted-foreground">
        {v.validatedBy ? `${v.validatedBy} · ` : ""}
        {relTime(v.validatedAt)}
      </span>
      {(v.scope || v.findings || v.conditions) && (
        <div className="w-full space-y-0.5 text-xs text-muted-foreground">
          {v.scope && <div>Scope: {v.scope}</div>}
          {v.findings && <div>Findings: {v.findings}</div>}
          {v.conditions && (
            <div>
              Conditions: {v.conditions}{" "}
              {v.outcome === "conditional" &&
                (v.conditionsClearedAt ? (
                  <span className="label-caps">cleared {fmt(v.conditionsClearedAt)}</span>
                ) : (
                  <span className="label-caps">outstanding</span>
                ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * The model-risk panel (§20.10). On the version page it carries this version's state and its
 * validation timeline; on the model page, the model's state (about its subject version) and
 * the latest validation. The header is the regime, as on the EU panel.
 */
export function MRMPanel({
  c,
  validations,
  version,
}: {
  c: Classification | null;
  // Given on the version page: this version's own history and state.
  validations?: VersionValidations | null;
  // The version the page is about, so the panel can say when the model's state is about a
  // different one.
  version?: string;
}) {
  const header = (
    <div>
      <div className="text-[0.9375rem] font-semibold">Model risk</div>
      <div className="text-xs text-muted-foreground">SR 26-2 · PRA SS1/23 · OSFI E-23</div>
    </div>
  );
  const state = (validations?.state ?? c?.state ?? "untiered") as MRMState;
  const reasons = validations ? validations.staleReasons : c?.staleReasons;
  const history = validations?.items ?? (c?.latestValidation ? [c.latestValidation] : []);

  return (
    <div className="mb-6 border border-border rounded-lg">
      <div className="p-4">
        <div className="mb-3 flex items-center justify-between gap-3">
          {header}
          <div className="flex items-center gap-2">
            <TierBadge tier={c?.mrmTier ?? "untiered"} />
            <MRMStateBadge state={state} />
          </div>
        </div>
        {!c ? (
          <p className="text-sm text-muted-foreground">
            Untiered. Nobody has stated a model-risk tier for this model — which is a real answer,
            not a low one.
          </p>
        ) : (
          <div className="grid gap-4 text-sm sm:grid-cols-2">
            <Field label="Tier">{TIER_LABEL[c.mrmTier ?? "untiered"]}</Field>
            <Field label="Basis">{c.basis || <span className="text-muted-foreground">not stated</span>}</Field>
            <Field label="Tiered">
              <span>{fmt(c.classifiedAt)}</span>
              {c.classifiedBy && <span className="text-muted-foreground"> by {c.classifiedBy}</span>}
            </Field>
            <Field label={validations ? "State of this version" : "State"}>
              {STATE_LABEL[state]}
              {!validations && c.version && (
                <span className="text-muted-foreground">
                  {" "}
                  · about <span className="font-mono">{c.version}</span>
                </span>
              )}
              {validations && c.version && version && c.version !== version && (
                // The model's state is about another version; say so rather than let the two
                // badges look like they disagree.
                <span className="text-muted-foreground">
                  {" "}
                  · the model as a whole is judged on <span className="font-mono">{c.version}</span> ({STATE_LABEL[c.state as MRMState].toLowerCase()})
                </span>
              )}
            </Field>
          </div>
        )}
        {c && <Reasons state={state} reasons={reasons} />}
      </div>

      <div className="border-t px-4 py-2.5">
        <div className="label-caps">{validations ? "Validations" : "Latest validation"}</div>
      </div>
      {history.length === 0 ? (
        <div className="border-t px-4 py-3 text-sm text-muted-foreground">No validation recorded.</div>
      ) : (
        <div className="border-t">
          {history.map((v, i) => (
            <ValidationRow key={v.id} v={v} superseded={i > 0} />
          ))}
        </div>
      )}
    </div>
  );
}
