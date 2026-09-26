import { Link, useSearchParams } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ClassificationCell } from "@/components/Classification";
import { MRMCell } from "@/components/MRM";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { relTime } from "@/lib/utils";

// The model table stays the "what do we have" page. It carries the risk class because that is
// a property of the model a reader wants at a glance — but the compliance *workflow* (grouping
// by exposure, the drift queue, the counts) lives on /compliance, so this page is not two
// pages fighting for one table.
//
// The classification filters are still honoured from the URL, so a link from the Compliance
// page lands on a filtered table. There are no filter controls here; the header says what is
// being filtered so a filtered view never looks like the whole list.

const FILTER_KEYS = ["euSystemRiskClass", "euGpaiTier", "classificationState", "mrmTier", "mrmState"] as const;

const FILTER_LABEL: Record<(typeof FILTER_KEYS)[number], string> = {
  euSystemRiskClass: "risk class",
  euGpaiTier: "GPAI tier",
  classificationState: "status",
  mrmTier: "model-risk tier",
  mrmState: "model-risk status",
};

export default function Models() {
  const [params] = useSearchParams();
  const q = params.get("q") ?? "";
  const filters = Object.fromEntries(
    FILTER_KEYS.map((k) => [k, params.get(k) ?? ""]).filter(([, v]) => v),
  ) as Record<string, string>;

  const { data, error, loading } = useAsync(
    () => api.models(q, filters),
    [q, filters.euSystemRiskClass, filters.euGpaiTier, filters.classificationState, filters.mrmTier, filters.mrmState],
  );

  const active = Object.entries(filters).map(
    ([k, v]) => `${FILTER_LABEL[k as (typeof FILTER_KEYS)[number]]} ${v.replace(/_/g, " ")}`,
  );
  if (q) active.unshift(`name “${q}”`);
  const sub = active.length ? `Filtered by ${active.join(" · ")}` : "System of record for every model";

  return (
    <div>
      <PageHeader
        title="Models"
        sub={sub}
        right={
          active.length ? (
            <Link to="/models" className="text-sm underline underline-offset-4">
              Clear filters
            </Link>
          ) : undefined
        }
      />
      {loading ? (
        <Loading />
      ) : error ? (
        <ErrorNote error={error} />
      ) : !data || data.items.length === 0 ? (
        <Empty>No models{active.length ? " match these filters" : " yet"}.</Empty>
      ) : (
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Owner</TableHead>
                <TableHead>EU risk class</TableHead>
                <TableHead>Model-risk tier</TableHead>
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
                  {/* Class at a glance; the stale badge links through to the worklist,
                      where the reason and the fix are. */}
                  <TableCell className="text-sm">
                    <ClassificationCell c={m.classification} />
                  </TableCell>
                  {/* The second regime's lens on the same table (§20.10). */}
                  <TableCell className="text-sm">
                    <MRMCell model={m.name} c={m.mrm} />
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">{m.versionCount}</TableCell>
                  <TableCell className="font-mono">
                    {m.production || <span className="text-muted-foreground">—</span>}
                  </TableCell>
                  <TableCell>
                    {m.state === "ARCHIVED" ? (
                      <span className="label-caps">archived</span>
                    ) : (
                      <span className="label-caps">active</span>
                    )}
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
