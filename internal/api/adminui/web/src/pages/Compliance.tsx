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
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
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

const CLASS_TITLE: Record<EUSystemRiskClass, string> = {
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
    <div className="bg-card px-4 py-4">
      <div className="label-caps">{label}</div>
      <div className="mt-1 font-mono text-2xl font-semibold tabular-nums">{value}</div>
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

export default function Compliance() {
  const { data, error, loading, reload } = useAsync(() => api.models(), []);
  const [editing, setEditing] = useState<ModelRollup | null>(null);
  const [showInventory, setShowInventory] = useState(false);

  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;

  const models = data.items;
  // The worklist merges the two states that need a human, but keeps them distinguishable in
  // the row itself — they are not the same problem, and §16.4 is emphatic that
  // `unclassified` is not a kind of `stale`. Stale sorts first: someone already did the work
  // once, so it is the cheaper fix.
  const stale = models.filter((m) => stateOf(m) === "stale");
  const never = models.filter((m) => stateOf(m) === "unclassified");
  const work = [...stale, ...never];
  const covered = models.filter((m) => stateOf(m) === "current");
  const highRisk = models.filter((m) =>
    ["high_annex_i", "high_annex_iii", "prohibited"].includes(classOf(m)),
  );

  const groups = CLASS_ORDER.map((cls) => ({
    cls,
    models: models.filter((m) => classOf(m) === cls),
  })).filter((g) => g.models.length > 0);

  return (
    <div>
      <PageHeader title="Compliance" sub="EU AI Act · risk classification and drift" />

      {/* Whether the record can be trusted comes before what the record says. §19.8 keeps
          this off model pages because it describes the install, not any one model. */}
      <div className="mb-6">
        <EvidencePanel />
      </div>

      {models.length === 0 ? (
        <Empty>No models yet.</Empty>
      ) : (
        <>
          <div className="grid grid-cols-2 gap-px border bg-border md:grid-cols-4">
            <Stat
              label="Needs attention"
              value={work.length}
              note={`${stale.length} drifted · ${never.length} never classified`}
            />
            <Stat label="Covered" value={covered.length} note="classified, nothing changed since" />
            <Stat label="High risk" value={highRisk.length} note="Annex I, Annex III or prohibited" />
            <Stat label="Models" value={models.length} />
          </div>

          <section className="mt-6">
            <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
              <h2 className="text-sm font-semibold">Needs attention</h2>
              <span className="text-xs text-muted-foreground">
                Recorded, never enforced — nothing here blocks a publish or a promotion.
              </span>
            </div>
            <div className="border bg-card">
              {work.length === 0 ? (
                <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                  Every model has a current classification. Nothing to revisit.
                </div>
              ) : (
                work.map((m) => <WorkRow key={m.id} m={m} onClassify={setEditing} />)
              )}
            </div>
          </section>

          {/* Art. 25 (§17.7). It sits under the classification worklist because it is the
              same job continued: the queue only exists for models somebody has classified
              governed, and it renders nothing when there are no derivations to look at. */}
          <ReviewQueue />

          {covered.length > 0 && (
            <section className="mt-6">
              <h2 className="mb-2 text-sm font-semibold">Covered</h2>
              <div className="border bg-card">
                {covered.map((m) => (
                  <div
                    key={m.id}
                    className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b px-4 py-2.5 last:border-b-0"
                  >
                    <div className="flex items-baseline gap-2.5">
                      <Link to={`/models/${m.name}`} className="hover:underline underline-offset-4">
                        {m.name}
                      </Link>
                      <RiskClassBadge c={m.classification} />
                    </div>
                    <div className="flex items-baseline gap-4 text-xs text-muted-foreground">
                      {m.classification?.reviewDueAt ? (
                        <span>review {fmtDate(m.classification.reviewDueAt)}</span>
                      ) : (
                        <span>no review scheduled</span>
                      )}
                      <button
                        onClick={() => setEditing(m)}
                        className="underline underline-offset-4 hover:text-foreground"
                      >
                        Re-classify
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          )}

          {/* Reference, not work. Collapsed by default so the taxonomy does not outrank the
              queue on first read. */}
          <section className="mt-6">
            <button
              onClick={() => setShowInventory((v) => !v)}
              className="flex w-full items-center justify-between border bg-card px-4 py-2.5 text-left text-sm hover:bg-accent"
            >
              <span className="font-semibold">Inventory by risk class</span>
              <span className="text-xs text-muted-foreground">
                {showInventory ? "Hide" : `${groups.length} classes · show`}
              </span>
            </button>
            {showInventory && (
              <div className="mt-3 grid gap-3 md:grid-cols-2">
                {groups.map(({ cls, models: group }) => (
                  <div key={cls} className="border bg-card">
                    <div className="flex items-center justify-between gap-3 border-b px-4 py-2.5">
                      <div className="flex items-center gap-2.5">
                        <span className="text-sm font-medium">{CLASS_TITLE[cls]}</span>
                        <ClassBadge cls={cls} />
                      </div>
                      {/* The count is the way through to the filtered table: the grouping
                          answers "how many", the table answers "which, with everything else
                          about them". */}
                      <Link
                        to={
                          cls === "unclassified"
                            ? "/models?classificationState=unclassified"
                            : `/models?euSystemRiskClass=${cls}`
                        }
                        className="font-mono text-sm tabular-nums text-muted-foreground hover:text-foreground hover:underline underline-offset-4"
                      >
                        {group.length}
                      </Link>
                    </div>
                    {group.map((m) => (
                      <div
                        key={m.id}
                        className="flex items-baseline justify-between gap-4 border-b px-4 py-2 text-sm last:border-b-0"
                      >
                        <Link
                          to={`/models/${m.name}`}
                          className="truncate hover:underline underline-offset-4"
                        >
                          {m.name}
                        </Link>
                        <span className="label-caps shrink-0">{relTime(m.updatedAt)}</span>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            )}
          </section>
        </>
      )}

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
