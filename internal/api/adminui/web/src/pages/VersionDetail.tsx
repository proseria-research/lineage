import { Link, useParams } from "react-router-dom";
import { Lock } from "lucide-react";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, useTab } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { StageBadge } from "@/components/StageBadge";
import { Term } from "@/components/Term";
import { StageActions } from "@/components/StageActions";
import { StageTrack } from "@/components/StageTrack";
import { LineageGraphView } from "@/components/LineageGraphView";
import { InsightPanel } from "@/components/InsightPanel";
import { CompliancePanel } from "@/components/Classification";
import { VersionReviews } from "@/components/Review";
import { MRMPanel, MRMStateBadge } from "@/components/MRM";
import { ConformanceBadge } from "@/components/ChangePlan";
import { VersionPortrait } from "@/components/VersionPortrait";
import { VersionFingerprint } from "@/components/VersionFingerprint";
import { HoldAction, HoldNote } from "@/components/Hold";
import { EventRow } from "@/components/ActivityFeed";
import { UsePanel } from "@/components/UsePanel";
import { HowItWorks, GUIDES } from "@/components/HowItWorks";
import { PageHeader, Loading, ErrorNote, Empty, SectionHeader } from "@/components/State";
import { humanize } from "@/lib/labels";
import { fmtBytes, relTime, shortDigest } from "@/lib/utils";

const TABS = [
  { id: "use", label: "Use" },
  { id: "overview", label: "Overview" },
  { id: "structure", label: "Structure & evaluations" },
  { id: "lineage", label: "Lineage" },
  { id: "governance", label: "Governance" },
  { id: "history", label: "History" },
];

export default function VersionDetail() {
  const { model = "", version = "" } = useParams();
  const [tab, setTab] = useTab(TABS);
  const { data, error, loading, reload } = useAsync(() => api.version(model, version), [model, version]);
  // The model's version list gives "previous version" for the compare link; plans give this
  // version's plan check. Both are side information — the page renders without them.
  const side = useAsync(
    () => Promise.all([api.model(model), api.changePlans()]).then(([m, p]) => ({ versions: m.versions, plans: p })),
    [model],
  );
  const graph = useAsync(() => api.graph(model, version, "both"), [model, version]);
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;
  const v = data.version;

  const idx = side.data?.versions.findIndex((x) => x.name === v.name) ?? -1;
  const previous = idx >= 0 ? side.data?.versions[idx + 1]?.name : undefined;
  const conformance = (side.data?.plans.items ?? []).filter((c) => c.model === model && c.version === v.name);
  const totalBytes = data.artifacts.reduce((n, a) => n + (a.sizeBytes ?? 0), 0);
  const mrmState = data.validations?.state;
  const governanceFlags =
    (mrmState === "stale" || mrmState === "unvalidated" ? 1 : 0) +
    conformance.filter((c) => c.conformance === "outside_plan" || c.conformance === "undetermined").length;

  return (
    <div>
      <PageHeader
        crumbs={[
          { label: "Models", to: "/models" },
          { label: model, to: `/models/${encodeURIComponent(model)}` },
          { label: v.name },
        ]}
        title={
          <span className="flex flex-wrap items-center gap-3">
            <span className="font-mono">{v.name}</span>
            <StageBadge stage={v.stage} />
            {v.lockedAt ? (
              <Term k="version_locked">
                <Badge variant="neutral" title={`Locked ${relTime(v.lockedAt)}`}>
                  <Lock className="h-3 w-3" aria-hidden />
                  Locked
                </Badge>
              </Term>
            ) : null}
          </span>
        }
        sub={
          v.description || (
            <>
              Published {relTime(v.createdAt)}
              {v.author ? ` by ${v.author}` : ""}
            </>
          )
        }
        right={
          <div className="flex flex-wrap items-center gap-2">
            <StageActions model={model} version={v.name} targets={data.allowedTargets} onDone={reload} />
            <HoldAction
              subject={{ model: data.model, version: v.name }}
              hold={v.legalHold}
              inherited={!v.legalHold && !!data.modelHold}
              onChanged={reload}
            />
          </div>
        }
      />

      {v.legalHold ? (
        <div className="mb-6">
          <HoldNote hold={v.legalHold} />
        </div>
      ) : data.modelHold ? (
        <div className="mb-6">
          <HoldNote hold={data.modelHold} heldSubject={`model/${data.model}`} />
        </div>
      ) : null}

      <div className="mb-8 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Summary label="Artifacts">
          {data.artifacts.length} {data.artifacts.length === 1 ? "file" : "files"}
          <Sub>{totalBytes ? fmtBytes(totalBytes) + " in total" : "no size reported"}</Sub>
        </Summary>
        <Summary label="Deployed">
          {data.deployments.length === 0 ? (
            <span className="text-muted-foreground">Nowhere</span>
          ) : (
            `${data.deployments.length} ${data.deployments.length === 1 ? "environment" : "environments"}`
          )}
          {data.deployments.length > 0 && <Sub>{data.deployments.map((d) => d.environment).join(", ")}</Sub>}
        </Summary>
        <Summary label="Compared with the previous version">
          {previous ? (
            <Link
              to={`/models/${encodeURIComponent(model)}/compare?from=${encodeURIComponent(previous)}&to=${encodeURIComponent(v.name)}`}
              className="text-brand hover:underline"
            >
              See what changed from <span className="font-mono">{previous}</span>
            </Link>
          ) : (
            <span className="text-muted-foreground">First version</span>
          )}
        </Summary>
        <Summary label="Governance">
          {governanceFlags === 0 ? (
            <span className="flex items-center gap-2">
              <span className="h-2 w-2 rounded-full bg-ok" />
              Nothing outstanding
            </span>
          ) : (
            <button onClick={() => setTab("governance")} className="flex flex-wrap items-center gap-1.5 text-left">
              {mrmState && <MRMStateBadge state={mrmState} />}
              {conformance
                .filter((c) => c.conformance === "outside_plan" || c.conformance === "undetermined")
                .map((c) => (
                  <ConformanceBadge key={c.edgeId} c={c.conformance} />
                ))}
            </button>
          )}
        </Summary>
      </div>

      <Tabs
        tabs={TABS.map((t) =>
          t.id === "governance" && governanceFlags > 0
            ? { ...t, hint: <span className="h-2 w-2 rounded-full bg-warn" aria-label="needs attention" /> }
            : t,
        )}
        value={tab}
        onChange={setTab}
      />

      {tab === "overview" && (
        <div className="space-y-6">
          <Card>
            <CardHeader>
              <CardTitle>Lifecycle</CardTitle>
              <p className="text-sm text-muted-foreground">Where this version has been, and when it got there.</p>
            </CardHeader>
            <CardContent>
              <StageTrack version={v} audit={data.audit} />
            </CardContent>
          </Card>

          <Card className="overflow-hidden">
            <CardHeader>
              <CardTitle>Artifacts</CardTitle>
              <p className="text-sm text-muted-foreground">The files that make up this version. Consumers fetch these when they resolve it.</p>
            </CardHeader>
            {data.artifacts.length === 0 ? (
              <CardContent>
                <Empty>No artifacts registered.</Empty>
              </CardContent>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead>Name</TableHead>
                    <TableHead>Kind</TableHead>
                    <TableHead>Format</TableHead>
                    <TableHead className="text-right">Size</TableHead>
                    <TableHead>Digest</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.artifacts.map((a) => (
                    <TableRow key={a.id}>
                      <TableCell className="font-mono text-[0.8125rem] font-medium">{a.name}</TableCell>
                      <TableCell>
                        <Badge variant="neutral">{humanize(a.kind.toLowerCase())}</Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {a.modelFormat?.name
                          ? `${a.modelFormat.name}${a.modelFormat.version ? " " + a.modelFormat.version : ""}`
                          : "—"}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">{fmtBytes(a.sizeBytes)}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground" title={a.digest}>
                        {shortDigest(a.digest)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Deployments</CardTitle>
              <p className="text-sm text-muted-foreground">Where this version is recorded as running.</p>
            </CardHeader>
            <CardContent>
              {data.deployments.length === 0 ? (
                <Empty>Not deployed anywhere.</Empty>
              ) : (
                <ul className="divide-y rounded-md border">
                  {data.deployments.map((d) => (
                    <li key={d.id} className="flex items-center justify-between gap-3 px-4 py-2.5 text-sm">
                      <span className="font-medium">{d.environment}</span>
                      <span className="truncate font-mono text-xs text-muted-foreground">
                        {d.endpointUri || d.externalRef || "—"}
                      </span>
                      <Badge variant={d.status === "ACTIVE" || d.status === "active" ? "ok" : "neutral"}>
                        {humanize(String(d.status).toLowerCase())}
                      </Badge>
                    </li>
                  ))}
                </ul>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {tab === "use" && (
        <UsePanel
          model={model}
          version={v.name}
          artifacts={data.artifacts}
          format={data.artifacts.find((a) => a.kind === "MODEL")?.modelFormat?.name}
        />
      )}

      {tab === "structure" && (
        <div className="space-y-6">
          <p className="max-w-3xl text-sm text-muted-foreground">
            Facts about what this version is made of, reported by the tools that built or scanned it. The
            fingerprint identifies the version; the portrait draws its layers.
          </p>
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,19rem)_minmax(0,1fr)]">
            <VersionFingerprint insight={data.insight} />
            <VersionPortrait insight={data.insight} />
          </div>
          <InsightPanel insight={data.insight} footprints={data.footprints} evaluations={data.evaluations} />
        </div>
      )}

      {tab === "lineage" && (
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>Lineage</CardTitle>
            <p className="text-sm text-muted-foreground">
              What this version was built from on the left, and what was built from it on the right.
            </p>
          </CardHeader>
          {graph.loading ? (
            <CardContent>
              <Loading label="Loading lineage" />
            </CardContent>
          ) : graph.error ? (
            <CardContent>
              <ErrorNote error={graph.error} />
            </CardContent>
          ) : (
            <LineageGraphView graph={graph.data} empty="No lineage recorded for this version." />
          )}
        </Card>
      )}

      {tab === "governance" && (
        <div className="space-y-6">
          <HowItWorks id="version" guide={GUIDES.version} />
          <CompliancePanel c={data.classification} model={data.model} onSaved={reload} />
          <VersionReviews reviews={data.reviews} />
          <MRMPanel c={data.mrm} validations={data.validations} version={v.name} model={model} artifacts={data.artifacts} onChanged={reload} />
          <section>
            <SectionHeader
              title="Change control"
              sub="Whether the change this version made is one its model's change plan allows."
            />
            {conformance.length === 0 ? (
              <Empty>No change plan applies to this version.</Empty>
            ) : (
              <ul className="divide-y rounded-lg border bg-card">
                {conformance.map((c) => (
                  <li key={c.edgeId} className="flex flex-wrap items-center justify-between gap-3 px-5 py-3 text-sm">
                    <span>
                      From{" "}
                      <span className="font-mono">
                        {c.derivedFrom ? `${c.derivedFrom.model} ${c.derivedFrom.version}` : c.derivedFromRef}
                      </span>
                      {c.plan?.ref && <span className="text-muted-foreground"> · plan {c.plan.ref}</span>}
                    </span>
                    <ConformanceBadge c={c.conformance} />
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      )}

      {tab === "history" && (
        <>
          <SectionHeader title="History" sub="Every recorded change to this version, newest first." />
          {data.audit.length === 0 ? (
            <Empty>Nothing recorded yet.</Empty>
          ) : (
            <ul className="divide-y overflow-hidden rounded-lg border bg-card">
              {data.audit.map((e) => (
                <EventRow key={e.id} e={e} />
              ))}
            </ul>
          )}
        </>
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

function Sub({ children }: { children: React.ReactNode }) {
  return <div className="mt-0.5 truncate text-xs font-normal text-muted-foreground">{children}</div>;
}
