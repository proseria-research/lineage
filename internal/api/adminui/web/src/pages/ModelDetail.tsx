import { Link, useParams } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { StageBadge } from "@/components/StageBadge";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { fmtTime, relTime } from "@/lib/utils";

export default function ModelDetail() {
  const { model = "" } = useParams();
  const { data, error, loading } = useAsync(() => api.model(model), [model]);
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;
  const m = data.model;

  return (
    <div>
      <PageHeader
        title={m.name}
        sub={m.owner ? `Owned by ${m.owner}` : undefined}
        right={
          <div className="text-right">
            <div className="label-caps">Production</div>
            <div className="font-mono text-sm">{data.production || "—"}</div>
          </div>
        }
      />

      <div className="mb-6 grid grid-cols-2 gap-px border bg-border sm:grid-cols-4">
        {[
          ["Model ID", <span className="font-mono">{m.id}</span>],
          ["State", m.state],
          ["Versions", String(m.versionCount)],
          ["Updated", fmtTime(m.updatedAt)],
        ].map(([k, v], i) => (
          <div key={i} className="bg-card px-3 py-2">
            <div className="label-caps">{k}</div>
            <div className="mt-0.5 truncate text-sm">{v}</div>
          </div>
        ))}
      </div>

      {m.labels && Object.keys(m.labels).length > 0 && (
        <div className="mb-6 flex flex-wrap gap-1.5">
          {Object.entries(m.labels).map(([k, v]) => (
            <span key={k} className="border px-1.5 py-0.5 font-mono text-xs">
              {k}={v}
            </span>
          ))}
        </div>
      )}

      <div className="label-caps mb-2">Versions</div>
      {data.versions.length === 0 ? (
        <Empty>No versions published.</Empty>
      ) : (
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Version</TableHead>
                <TableHead>Stage</TableHead>
                <TableHead>Author</TableHead>
                <TableHead className="text-right">Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.versions.map((v) => (
                <TableRow key={v.id}>
                  <TableCell className="font-mono font-medium">
                    <Link to={`/models/${model}/versions/${v.name}`} className="hover:underline underline-offset-4">
                      {v.name}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <StageBadge stage={v.stage} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">{v.author || "—"}</TableCell>
                  <TableCell className="text-right label-caps">{relTime(v.createdAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  );
}
