import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Tabs, useTab } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { VersionBoard } from "@/components/VersionBoard";
import { HoldAction, HoldNote } from "@/components/Hold";
import { MRMPanel, MRMCell } from "@/components/MRM";
import { ClassificationCell } from "@/components/Classification";
import { PageHeader, Loading, ErrorNote, Empty, SectionHeader } from "@/components/State";
import { Button } from "@/components/ui/button";
import { ChangePlanDialog } from "@/components/GovernanceDialogs";
import { VERDICT_LABEL } from "@/components/Review";
import { fmtTime, relTime } from "@/lib/utils";

const TABS = [
  { id: "versions", label: "Versions" },
  { id: "governance", label: "Governance" },
  { id: "details", label: "Details" },
];

export default function ModelDetail() {
  const { model = "" } = useParams();
  const [tab, setTab] = useTab(TABS);
  const { data, error, loading, reload } = useAsync(() => api.model(model), [model]);
  const plans = useAsync(() => api.changePlans().then((p) => p.plans.filter((x) => x.model === model)), [model]);
  const [planDialog, setPlanDialog] = useState(false);
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;
  const m = data.model;
  const openPlan = plans.data?.find((p) => p.effectiveTo == null) ?? null;
  const prod = data.versions.find((v) => v.name === data.production);

  return (
    <div>
      <PageHeader
        crumbs={[{ label: "Models", to: "/models" }, { label: m.name }]}
        title={
          <span className="flex flex-wrap items-center gap-3">
            {m.name}
            {m.state === "ARCHIVED" && <Badge variant="dashed">Archived</Badge>}
          </span>
        }
        sub={m.owner ? `Owned by ${m.owner}` : "No owner recorded"}
        right={<HoldAction subject={{ model: m.name }} hold={m.legalHold} onChanged={reload} />}
      />

      {m.legalHold ? (
        <div className="mb-6">
          <HoldNote hold={m.legalHold} />
        </div>
      ) : null}

      {/* The four answers a reader comes for, before any detail. */}
      <div className="mb-8 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Summary label="In production">
          {prod ? (
            <Link
              to={`/models/${encodeURIComponent(m.name)}/versions/${encodeURIComponent(prod.name)}`}
              className="flex items-center gap-2 hover:underline"
            >
              <span className="h-2 w-2 rounded-full bg-ok" />
              <span className="font-mono">{prod.name}</span>
            </Link>
          ) : (
            <span className="text-muted-foreground">Not released</span>
          )}
          {prod && <div className="mt-0.5 text-xs text-muted-foreground">since {relTime(prod.updatedAt)}</div>}
        </Summary>
        <Summary label="Versions">
          <span className="tabular-nums">{m.versionCount}</span>
          <div className="mt-0.5 text-xs text-muted-foreground">last change {relTime(m.updatedAt)}</div>
        </Summary>
        <Summary label="EU AI Act risk">
          <ClassificationCell c={m.classification} />
        </Summary>
        <Summary label="Model risk">
          <MRMCell model={m.name} c={m.mrm} />
        </Summary>
      </div>

      <Tabs tabs={TABS} value={tab} onChange={setTab} />

      {tab === "versions" &&
        (data.versions.length === 0 ? (
          <Empty>No versions published yet.</Empty>
        ) : (
          <>
            <SectionHeader
              title="Versions by stage"
              sub="Versions move left to right as they're tested and released. Open one for its artifacts, lineage and history."
            />
            <VersionBoard model={model} versions={data.versions} />
          </>
        ))}

      {tab === "governance" && (
        <div className="space-y-6">
          <MRMPanel c={m.mrm} model={m.name} onChanged={reload} />

          <section>
            <SectionHeader
              title="Change control plan"
              sub="The kinds of change this model may make without a fresh review. New versions are checked against it; nothing is blocked."
              right={
                plans.data && (
                  <Button size="sm" variant={openPlan ? "outline" : "default"} onClick={() => setPlanDialog(true)}>
                    {openPlan ? "Replace plan" : "Declare a plan"}
                  </Button>
                )
              }
            />
            {!plans.data || plans.data.length === 0 ? (
              <Empty>No change plan declared for this model.</Empty>
            ) : (
              <ul className="divide-y rounded-lg border bg-card">
                {plans.data.map((p) => {
                  const closed = p.effectiveTo != null;
                  return (
                    <li key={p.id} className="px-5 py-3.5">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{p.ref || "Unnamed plan"}</span>
                        {closed ? <Badge variant="dashed">Replaced</Badge> : <Badge variant="ok">In force</Badge>}
                        <span className="ml-auto text-xs text-muted-foreground">
                          {fmtDate(p.effectiveFrom)} – {closed ? fmtDate(p.effectiveTo!) : "now"}
                          {p.declaredBy ? `, declared by ${p.declaredBy}` : ""}
                        </span>
                      </div>
                      <p className="mt-1 text-sm">{p.summary}</p>
                      <p className="mt-0.5 text-xs text-muted-foreground">
                        Allows {p.allowedVerdicts.map((v) => VERDICT_LABEL[v].toLowerCase()).join(", ")} changes
                        {p.allowedMethods?.length ? `, made by ${p.allowedMethods.join(" or ")}` : ", by any method"}.
                      </p>
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
          <p className="text-sm text-muted-foreground">
            The EU AI Act classification is set per model on the{" "}
            <Link to="/compliance?tab=eu" className="font-medium text-brand hover:underline">
              Governance
            </Link>{" "}
            page. Per-version reviews, validations and plan checks are on each version's page.
          </p>
        </div>
      )}

      {tab === "details" && (
        <div className="rounded-lg border bg-card">
          <dl className="divide-y">
            <Row k="Model ID">
              <span className="font-mono text-[0.8125rem]">{m.id}</span>
            </Row>
            <Row k="Status">{m.state === "ARCHIVED" ? "Archived" : "Active"}</Row>
            <Row k="Last updated">{fmtTime(m.updatedAt)}</Row>
            <Row k="Labels">
              {Object.keys(m.labels ?? {}).length === 0 ? (
                <span className="text-muted-foreground">None</span>
              ) : (
                <span className="flex flex-wrap gap-1.5">
                  {Object.entries(m.labels ?? {}).map(([k, v]) => (
                    <span key={k} className="rounded-md bg-secondary px-2 py-0.5 font-mono text-xs">
                      {k}={v}
                    </span>
                  ))}
                </span>
              )}
            </Row>
          </dl>
        </div>
      )}
      {planDialog && (
        <ChangePlanDialog
          model={m.name}
          open={openPlan}
          onClose={() => setPlanDialog(false)}
          onSaved={() => {
            setPlanDialog(false);
            plans.reload();
          }}
        />
      )}
    </div>
  );
}

const fmtDate = (ms: number) => new Date(ms).toISOString().slice(0, 10);

function Summary({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border bg-card px-4 py-3.5">
      <div className="mb-1.5 text-xs text-muted-foreground">{label}</div>
      <div className="text-[0.9375rem] font-medium">{children}</div>
    </div>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-1 px-5 py-3 sm:grid-cols-[12rem_1fr]">
      <dt className="text-sm text-muted-foreground">{k}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}
