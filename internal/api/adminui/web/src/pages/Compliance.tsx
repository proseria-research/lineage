import { useState } from "react";
import { Link } from "react-router-dom";
import { ChevronRight } from "lucide-react";
import { api, type EUSystemRiskClass, type ModelRollup } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Button } from "@/components/ui/button";
import { ClassBadge, RiskClassBadge, STALE_REASON_TEXT } from "@/components/Classification";
import { ClassifyDialog } from "@/components/ClassifyDialog";
import { EvidencePanel } from "@/components/Hold";
import { ReviewQueue } from "@/components/Review";
import { MRMStateBadge, TierBadge } from "@/components/MRM";
import { ChangePlanSection } from "@/components/ChangePlan";
import { PageHeader, Loading, ErrorNote, Empty, SectionHeader } from "@/components/State";
import { Tabs, useTab } from "@/components/ui/tabs";
import { AttentionList } from "@/components/Attention";
import { attentionItems } from "@/lib/attention";
import { GLOSSARY } from "@/lib/labels";
import { relTime } from "@/lib/utils";

// The compliance workspace (§16.9), organised around what a person has to *do* rather than
// around the Act's taxonomy. Two questions, in this order:
//
//   1. What needs my attention?  — never classified, or classified and since drifted.
//   2. What is our exposure?     — the grouped inventory, as reference, collapsed.
//
// The taxonomy view is real and stays, but it is reference material; leading with it made a
// worklist read like a filing cabinet.
//
// Nothing here decides or changes a class on its own. The only way anything moves is someone
// opening the form and making an assessment (§16.2).

const CLASS_ORDER: EUSystemRiskClass[] = [
  "prohibited",
  "high_annex_i",
  "high_annex_iii",
  "limited",
  "minimal",
  "unclassified",
];

export const CLASS_TITLE: Record<EUSystemRiskClass, string> = {
  prohibited: "Prohibited",
  high_annex_i: "High risk · Annex I",
  high_annex_iii: "High risk · Annex III",
  limited: "Limited risk",
  minimal: "Minimal risk",
  unclassified: "Unclassified",
};

const classOf = (m: ModelRollup): EUSystemRiskClass =>
  m.classification?.euSystemRiskClass ?? "unclassified";

const stateOf = (m: ModelRollup) => m.classification?.state ?? "unclassified";

const fmtDate = (ms: number) => new Date(ms).toISOString().slice(0, 10);

/** Why this model is in the worklist, in the words the reader needs to act on. */
function whyText(m: ModelRollup): string {
  const c = m.classification;
  if (!c || c.state === "unclassified") {
    return "Never classified — nobody has stated a risk class for this model.";
  }
  return (c.staleReasons ?? []).map((r) => STALE_REASON_TEXT[r] ?? r).join("; ");
}

function Stat({ label, value, note }: { label: string; value: number; note?: string }) {
  return (
    <div className="rounded-lg border bg-card px-4 py-3.5">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
      {note && <div className="mt-1 text-xs text-muted-foreground">{note}</div>}
    </div>
  );
}

/** A worklist row: what it is, why it is here, and the one action that resolves it. */
function WorkRow({ m, onClassify }: { m: ModelRollup; onClassify: (m: ModelRollup) => void }) {
  const never = stateOf(m) === "unclassified";
  return (
    <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2 border-b px-4 py-3 last:border-b-0">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <Link to={`/models/${m.name}`} className="font-medium hover:underline underline-offset-4">
            {m.name}
          </Link>
          <RiskClassBadge c={m.classification} />
          {m.owner && <span className="text-xs text-muted-foreground">{m.owner}</span>}
          {m.production && (
            <span className="font-mono text-xs text-muted-foreground">prod {m.production}</span>
          )}
        </div>
        <div className="mt-1 text-sm text-muted-foreground">{whyText(m)}</div>
      </div>
      <Button size="sm" variant={never ? "default" : "outline"} onClick={() => onClassify(m)}>
        {never ? "Classify" : "Re-classify"}
        <ChevronRight className="h-3.5 w-3.5" strokeWidth={1.5} />
      </Button>
    </div>
  );
}

/**
 * The second regime's worklist (§20.10): tiered models whose validation is missing or has
 * gone stale, each with its reasons and a way through to the version they are about. It
 * renders nothing when no model is tiered — the regime is not in use on this install.
 */
function ModelRiskWork({ models }: { models: ModelRollup[] }) {
  const tiered = models.filter((m) => m.mrm && m.mrm.mrmTier !== "untiered");
  if (tiered.length === 0) return null;
  const work = tiered
    .filter((m) => m.mrm!.state === "stale" || m.mrm!.state === "unvalidated")
    // Stale first, as on the EU list: someone validated it once, so it is the cheaper fix.
    .sort((a, b) => (a.mrm!.state === b.mrm!.state ? 0 : a.mrm!.state === "stale" ? -1 : 1));

  return (
    <section className="mt-6">
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-semibold">Model risk · validation and monitoring</h2>
        <span className="text-xs text-muted-foreground">
          {tiered.length} tiered · {work.length} need attention
        </span>
      </div>
      <div className="border bg-card rounded-lg">
        {work.length === 0 ? (
          <div className="px-4 py-8 text-center text-sm text-muted-foreground">
            Every tiered model has a current validation and is being monitored.
          </div>
        ) : (
          work.map((m) => {
            const c = m.mrm!;
            const why =
              c.state === "unvalidated"
                ? c.latestValidation
                  ? `Latest validation is ${c.latestValidation.outcome}.`
                  : "No validation recorded."
                : (c.staleReasons ?? []).map((r) => STALE_REASON_TEXT[r] ?? r).join("; ");
            return (
              <div key={m.id} className="border-b px-4 py-3 last:border-b-0">
                <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
                  <Link to={`/models/${m.name}`} className="font-medium hover:underline underline-offset-4">
                    {m.name}
                  </Link>
                  <TierBadge tier={c.mrmTier ?? "untiered"} />
                  <MRMStateBadge state={c.state as "stale" | "unvalidated"} />
                  {c.version && (
                    <Link
                      to={`/models/${m.name}/versions/${c.version}`}
                      className="font-mono text-xs text-muted-foreground hover:underline underline-offset-4"
                    >
                      {c.version}
                    </Link>
                  )}
                </div>
                <div className="mt-1 text-sm text-muted-foreground">{why}</div>
              </div>
            );
          })
        )}
      </div>
    </section>
  );
}

const TABS = [
  { id: "todo", label: "To do" },
  { id: "eu", label: "EU AI Act" },
  { id: "reviews", label: "Modification reviews" },
  { id: "risk", label: "Model risk" },
  { id: "plans", label: "Change control" },
  { id: "integrity", label: "Audit integrity" },
];

const INTRO: Record<string, React.ReactNode> = {
  todo: "Everything across the governance programmes that needs a person to look at it. Nothing here blocks a release.",
  eu: GLOSSARY.euRiskClass + " A classification goes out of date when a new version ships, production changes, or its review date passes.",
  reviews: GLOSSARY.modificationReview,
  risk: GLOSSARY.modelRisk,
  plans: GLOSSARY.changePlan,
  integrity: GLOSSARY.auditIntegrity,
};

const Count = ({ n, tone = "warn" }: { n: number; tone?: "warn" | "danger" }) =>
  n > 0 ? (
    <span
      className={
        tone === "danger"
          ? "rounded-full bg-danger-soft px-1.5 text-xs tabular-nums text-danger"
          : "rounded-full bg-warn-soft px-1.5 text-xs tabular-nums text-warn"
      }
    >
      {n}
    </span>
  ) : null;

export default function Compliance() {
  const [tab, setTab] = useTab(TABS);
  const { data, error, loading, reload } = useAsync(
    () =>
      Promise.all([api.models(), api.reviews("open"), api.changePlans()]).then(([m, r, p]) => ({
        models: m.items,
        reviews: r.items,
        plans: p,
      })),
    [],
  );
  const [editing, setEditing] = useState<ModelRollup | null>(null);
  const [showInventory, setShowInventory] = useState(false);

  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;

  const models = data.models;
  const todo = attentionItems(models, data.reviews, data.plans.items);
  const byArea = (a: string) => todo.filter((t) => t.area === a).length;
  const stale = models.filter((m) => stateOf(m) === "stale");
  const never = models.filter((m) => stateOf(m) === "unclassified");
  const work = [...stale, ...never];
  const covered = models.filter((m) => stateOf(m) === "current");

  const groups = CLASS_ORDER.map((cls) => ({
    cls,
    models: models.filter((m) => classOf(m) === cls),
  })).filter((g) => g.models.length > 0);

  const tabs = TABS.map((t) => ({
    ...t,
    hint:
      t.id === "todo" ? (
        <Count n={todo.length} tone={todo.some((x) => x.severity === "danger") ? "danger" : "warn"} />
      ) : t.id === "eu" ? (
        <Count n={stale.length} />
      ) : t.id === "reviews" ? (
        <Count n={byArea("review")} />
      ) : t.id === "risk" ? (
        <Count n={byArea("mrm")} />
      ) : t.id === "plans" ? (
        <Count n={byArea("plan")} tone="danger" />
      ) : undefined,
  }));

  return (
    <div>
      <PageHeader
        title="Governance"
        sub="Risk classification, modification reviews, model risk, change plans and the integrity of the audit log. Lineage records what your team decided; it never decides for you."
      />

      <Tabs tabs={tabs} value={tab} onChange={setTab} />
      <p className="-mt-2 mb-6 max-w-3xl text-sm text-muted-foreground">{INTRO[tab]}</p>

      {models.length === 0 && tab !== "integrity" ? (
        <Empty>No models yet.</Empty>
      ) : (
        <>
          {tab === "todo" && (
            <AttentionList items={todo} emptyText="Nothing needs attention. Every assessment is current." />
          )}

          {tab === "eu" && (
            <>
              <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                <Stat label="Out of date" value={stale.length} note="changed since they were classified" />
                <Stat label="Not classified" value={never.length} note="nobody has assessed them yet" />
                <Stat label="Current" value={covered.length} note="classified, nothing changed since" />
                <Stat
                  label="High risk or prohibited"
                  value={models.filter((m) => ["high_annex_i", "high_annex_iii", "prohibited"].includes(classOf(m))).length}
                />
              </div>

              <section className="mt-8">
                <SectionHeader title="To classify or re-classify" />
                <div className="rounded-lg border bg-card">
                  {work.length === 0 ? (
                    <div className="px-5 py-8 text-center text-sm text-muted-foreground">
                      Every model has a current classification.
                    </div>
                  ) : (
                    work.map((m) => <WorkRow key={m.id} m={m} onClassify={setEditing} />)
                  )}
                </div>
              </section>

              {covered.length > 0 && (
                <section className="mt-8">
                  <SectionHeader title="Current" sub="Classified, and nothing has changed since." />
                  <div className="rounded-lg border bg-card">
                    {covered.map((m) => (
                      <div
                        key={m.id}
                        className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-b px-5 py-3 last:border-b-0"
                      >
                        <div className="flex items-center gap-2.5">
                          <Link to={`/models/${m.name}`} className="font-medium hover:underline">
                            {m.name}
                          </Link>
                          <RiskClassBadge c={m.classification} />
                        </div>
                        <div className="flex items-center gap-4 text-sm text-muted-foreground">
                          {m.classification?.reviewDueAt ? (
                            <span>Review due {fmtDate(m.classification.reviewDueAt)}</span>
                          ) : (
                            <span>No review scheduled</span>
                          )}
                          <Button size="sm" variant="outline" onClick={() => setEditing(m)}>
                            Re-classify
                          </Button>
                        </div>
                      </div>
                    ))}
                  </div>
                </section>
              )}

              <section className="mt-8">
                <button
                  onClick={() => setShowInventory((v) => !v)}
                  className="flex w-full items-center justify-between rounded-lg border bg-card px-5 py-3 text-left text-sm hover:bg-accent"
                >
                  <span className="font-semibold">All models by risk class</span>
                  <span className="text-muted-foreground">{showInventory ? "Hide" : "Show"}</span>
                </button>
                {showInventory && (
                  <div className="mt-3 grid gap-3 md:grid-cols-2">
                    {groups.map(({ cls, models: group }) => (
                      <div key={cls} className="rounded-lg border bg-card">
                        <div className="flex items-center justify-between gap-3 border-b px-4 py-2.5">
                          <ClassBadge cls={cls} />
                          <Link
                            to={
                              cls === "unclassified"
                                ? "/models?classificationState=unclassified"
                                : `/models?euSystemRiskClass=${cls}`
                            }
                            className="text-sm tabular-nums text-brand hover:underline"
                          >
                            {group.length} {group.length === 1 ? "model" : "models"}
                          </Link>
                        </div>
                        {group.map((m) => (
                          <div
                            key={m.id}
                            className="flex items-center justify-between gap-4 border-b px-4 py-2 text-sm last:border-b-0"
                          >
                            <Link to={`/models/${m.name}`} className="truncate hover:underline">
                              {m.name}
                            </Link>
                            <span className="shrink-0 text-xs text-muted-foreground">{relTime(m.updatedAt)}</span>
                          </div>
                        ))}
                      </div>
                    ))}
                  </div>
                )}
              </section>
            </>
          )}

          {tab === "reviews" &&
            (data.reviews.length === 0 && byArea("review") === 0 ? (
              <>
                <ReviewQueue />
                <Empty>No derivations of high-risk models are waiting for review.</Empty>
              </>
            ) : (
              <ReviewQueue />
            ))}

          {tab === "risk" &&
            (models.some((m) => m.mrm && m.mrm.mrmTier !== "untiered") ? (
              <ModelRiskWork models={models} />
            ) : (
              <Empty>No model has a model-risk tier yet. Tiers are set through the Model API.</Empty>
            ))}

          {tab === "plans" &&
            (data.plans.plans.length === 0 ? (
              <Empty>No model has a change plan yet. Plans are declared through the Model API.</Empty>
            ) : (
              <ChangePlanSection />
            ))}
        </>
      )}

      {tab === "integrity" && <EvidencePanel />}

      {editing && (
        <ClassifyDialog
          model={editing.name}
          current={editing.classification}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
    </div>
  );
}
