import { Link, useParams } from "react-router-dom";
import { ArrowRight } from "lucide-react";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { StageBadge } from "@/components/StageBadge";
import { StageActions } from "@/components/StageActions";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { fmtBytes, relTime, shortDigest } from "@/lib/utils";

export default function VersionDetail() {
  const { model = "", version = "" } = useParams();
  const { data, error, loading, reload } = useAsync(() => api.version(model, version), [model, version]);
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;
  const v = data.version;

  return (
    <div>
      <PageHeader
        title={
          <span>
            <Link to={`/models/${model}`} className="text-muted-foreground hover:underline">
              {model}
            </Link>
            <span className="mx-1.5 text-muted-foreground">/</span>
            <span className="font-mono">{v.name}</span>
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

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* Lineage */}
        <Card>
          <CardHeader>
            <CardTitle>Lineage</CardTitle>
          </CardHeader>
          <CardContent>
            {data.lineage.length === 0 ? (
              <Empty>No lineage edges.</Empty>
            ) : (
              <ul className="space-y-2">
                {data.lineage.map((e) => (
                  <li key={e.id} className="flex items-center gap-2 border px-2.5 py-1.5 text-sm">
                    <span className="font-mono text-muted-foreground">{e.srcId === v.id ? v.name : e.srcId.slice(0, 8)}</span>
                    <span className="label-caps flex items-center gap-1">
                      <ArrowRight className="h-3 w-3" strokeWidth={1.5} />
                      {e.relation}
                    </span>
                    <span className="truncate font-mono">{e.dstRef || e.dstId?.slice(0, 8) || "—"}</span>
                  </li>
                ))}
              </ul>
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
      </div>

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
