import { Link, useParams } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { StageBadge } from "@/components/StageBadge";
import { StageActions } from "@/components/StageActions";
import { StageTrack } from "@/components/StageTrack";
import { Cube } from "@/components/Cube";
import { LineageGraphView } from "@/components/LineageGraphView";
import { InsightPanel } from "@/components/InsightPanel";
import { VersionPortrait } from "@/components/VersionPortrait";
import { VersionFingerprint } from "@/components/VersionFingerprint";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { fmtBytes, relTime, shortDigest } from "@/lib/utils";

export default function VersionDetail() {
  const { model = "", version = "" } = useParams();
  const { data, error, loading, reload } = useAsync(() => api.version(model, version), [model, version]);
  const graph = useAsync(() => api.graph(model, version, "both"), [model, version]);
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;
  const v = data.version;

  return (
    <div>
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            <Cube size={22} className="text-muted-foreground" />
            <span>
              <Link to={`/models/${model}`} className="text-muted-foreground hover:underline">
                {model}
              </Link>
              <span className="mx-1.5 text-muted-foreground">/</span>
              <span className="font-mono">{v.name}</span>
            </span>
          </span>
        }
        right={
          <div className="flex flex-col items-end gap-2">
            <StageBadge stage={v.stage} />
            <StageActions model={model} version={v.name} targets={data.allowedTargets} onDone={reload} />
          </div>
        }
      />

      {v.description && <p className="mb-5 max-w-2xl text-sm text-muted-foreground">{v.description}</p>}

      {/* Top row: what this version *is* (§12.4, identity) beside where it sits (§02.4).
          The fingerprint is square and compact; the lifecycle takes the remaining width. */}
      <div className="mb-6 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,19rem)_minmax(0,1fr)]">
        <VersionFingerprint insight={data.insight} />
        <Card>
          <CardHeader>
            <CardTitle>Lifecycle</CardTitle>
          </CardHeader>
          <CardContent>
            <StageTrack version={v} audit={data.audit} />
          </CardContent>
        </Card>
      </div>

      {/* The structure drawing (§12.3), full width — a left-to-right stack wants the axis. */}
      <VersionPortrait insight={data.insight} />

      {/* Artifacts */}
      <Card className="mb-6">
        <CardHeader>
          <CardTitle>Artifacts</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {data.artifacts.length === 0 ? (
            <div className="p-4">
              <Empty>No artifacts.</Empty>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
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
                    <TableCell className="font-mono font-medium">{a.name}</TableCell>
                    <TableCell className="label-caps">{a.kind}</TableCell>
                    <TableCell className="font-mono text-muted-foreground">
                      {a.modelFormat?.name ? `${a.modelFormat.name}${a.modelFormat.version ? " " + a.modelFormat.version : ""}` : "—"}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{fmtBytes(a.sizeBytes)}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">{shortDigest(a.digest)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Composition facts reported by producers (§11.8) */}
      <InsightPanel insight={data.insight} footprints={data.footprints} evaluations={data.evaluations} />

      {/* One neighbourhood: provenance to the left, impact to the right (§07.3). */}
      <Card className="mb-6">
        <CardHeader>
          <CardTitle>Lineage</CardTitle>
          <p className="text-xs text-muted-foreground">Trace what produced this version and what depends on it.</p>
        </CardHeader>
        <CardContent className="p-0">
          {graph.loading ? (
            <div className="px-4 py-8 text-center label-caps">Loading lineage…</div>
          ) : graph.error ? (
            <div className="m-4 border border-destructive/50 bg-muted px-3 py-2 text-sm">{graph.error}</div>
          ) : (
            <LineageGraphView graph={graph.data} empty="No recorded lineage." />
          )}
        </CardContent>
      </Card>

      {/* Deployments */}
      <Card>
        <CardHeader>
          <CardTitle>Deployments</CardTitle>
        </CardHeader>
        <CardContent>
          {data.deployments.length === 0 ? (
            <Empty>Not deployed.</Empty>
          ) : (
            <ul className="space-y-2">
              {data.deployments.map((d) => (
                <li key={d.id} className="flex items-center justify-between gap-2 border px-2.5 py-1.5 text-sm">
                  <span className="font-medium">{d.environment}</span>
                  <span className="truncate font-mono text-xs text-muted-foreground">{d.endpointUri || d.externalRef || "—"}</span>
                  <span className="label-caps shrink-0">{d.status}</span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      {/* Audit timeline */}
      <div className="label-caps mb-2 mt-6">Audit timeline</div>
      {data.audit.length === 0 ? (
        <Empty>No audit events.</Empty>
      ) : (
        <ol className="ml-1 border-l pl-4">
          {data.audit.map((e) => (
            <li key={e.id} className="relative py-2">
              <span className="absolute -left-[1.3125rem] top-3 h-1.5 w-1.5 border border-foreground bg-background" />
              <div className="flex items-baseline justify-between gap-3">
                <span className="text-sm">
                  <span className="font-mono text-muted-foreground">{e.action}</span>
                  <span className="mx-2 text-muted-foreground">·</span>
                  {e.summary}
                </span>
                <span className="label-caps shrink-0">{relTime(e.at)}</span>
              </div>
              {e.actor && <div className="label-caps mt-0.5">by {e.actor}</div>}
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
