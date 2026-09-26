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
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;
  const m = data.model;
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
      <div className="mb-8 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
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
          <MRMPanel c={m.mrm} />
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
    </div>
  );
}

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
