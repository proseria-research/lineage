import { Link, useSearchParams } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { StageBadge } from "@/components/StageBadge";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { relTime } from "@/lib/utils";

export default function Models() {
  const [params] = useSearchParams();
  const q = params.get("q") ?? "";
  const { data, error, loading } = useAsync(() => api.models(q), [q]);

  return (
    <div>
      <PageHeader title="Models" sub={q ? `Filtered by “${q}”` : "System of record for every model"} />
      {loading ? (
        <Loading />
      ) : error ? (
        <ErrorNote error={error} />
      ) : !data || data.items.length === 0 ? (
        <Empty>No models{q ? " match this search" : " yet"}.</Empty>
      ) : (
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Owner</TableHead>
                <TableHead className="text-right">Versions</TableHead>
                <TableHead>Production</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="text-right">Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.map((m) => (
                <TableRow key={m.id}>
                  <TableCell className="font-medium">
                    <Link to={`/models/${m.name}`} className="hover:underline underline-offset-4">
                      {m.name}
                    </Link>
                  </TableCell>
                  <TableCell className="text-muted-foreground">{m.owner || "—"}</TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{m.versionCount}</TableCell>
                  <TableCell className="font-mono">{m.production || <span className="text-muted-foreground">—</span>}</TableCell>
                  <TableCell>
                    {m.state === "ARCHIVED" ? <span className="label-caps">archived</span> : <span className="label-caps">active</span>}
                  </TableCell>
                  <TableCell className="text-right label-caps">{relTime(m.updatedAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  );
}
