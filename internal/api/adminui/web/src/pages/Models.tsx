import { Link, useSearchParams } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ClassificationCell } from "@/components/Classification";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { relTime } from "@/lib/utils";

// The model table is also the §16.9 inventory view — the "it takes us weeks to work out
// which AI systems are in production" question is asked of the same list, so it is answered
// on the same page rather than in a separate compliance silo.

const CLASS_FILTERS: { value: string; label: string }[] = [
  { value: "", label: "All classes" },
  { value: "high_annex_iii", label: "High · Annex III" },
  { value: "high_annex_i", label: "High · Annex I" },
  { value: "prohibited", label: "Prohibited" },
  { value: "limited", label: "Limited" },
  { value: "minimal", label: "Minimal" },
  { value: "unclassified", label: "Unclassified" },
];

const STATE_FILTERS: { value: string; label: string }[] = [
  { value: "", label: "Any state" },
  { value: "stale", label: "Stale" },
  { value: "current", label: "Current" },
  // Its own option, never folded into "stale" (§16.4).
  { value: "unclassified", label: "Unclassified" },
];

function Select({
  value,
  onChange,
  options,
  label,
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
  label: string;
}) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <span className="label-caps text-muted-foreground">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="border border-border bg-transparent px-2 py-1 text-sm"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}

export default function Models() {
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  // `unclassified` selected as a *class* means the row says so; selected as a *state* means
  // there may be no row at all. Both are legitimate questions, so both filters exist.
  const cls = params.get("euSystemRiskClass") ?? "";
  const state = params.get("classificationState") ?? "";

  const { data, error, loading } = useAsync(
    () => api.models(q, { euSystemRiskClass: cls, classificationState: state }),
    [q, cls, state],
  );

  const setFilter = (key: string) => (v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(key, v);
    else next.delete(key);
    setParams(next);
  };

  const filtered = cls || state;
  const staleCount = data?.items.filter((m) => m.classification?.state === "stale").length ?? 0;

  return (
    <div>
      <PageHeader
        title="Models"
        sub={q ? `Filtered by “${q}”` : "System of record for every model"}
      />

      <div className="mb-4 flex flex-wrap items-center gap-4">
        <Select label="Risk class" value={cls} onChange={setFilter("euSystemRiskClass")} options={CLASS_FILTERS} />
        <Select label="Status" value={state} onChange={setFilter("classificationState")} options={STATE_FILTERS} />
        {staleCount > 0 && (
          // A count, not a warning. A truthful "these need another look" must not read as a
          // failure the registry is accusing anyone of (§16.2).
          <span className="text-sm text-muted-foreground">
            {staleCount} classification{staleCount === 1 ? "" : "s"} to revisit
          </span>
        )}
      </div>

      {loading ? (
        <Loading />
      ) : error ? (
        <ErrorNote error={error} />
      ) : !data || data.items.length === 0 ? (
        <Empty>No models{q || filtered ? " match these filters" : " yet"}.</Empty>
      ) : (
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Owner</TableHead>
                <TableHead>EU risk class</TableHead>
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
                  <TableCell className="text-sm">
                    <ClassificationCell c={m.classification} />
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
