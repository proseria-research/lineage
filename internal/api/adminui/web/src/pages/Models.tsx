import { Link, useSearchParams } from "react-router-dom";
import { AlertTriangle } from "lucide-react";
import { api, type ModelRollup } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, useTab } from "@/components/ui/tabs";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ClassificationCell } from "@/components/Classification";
import { MRMCell } from "@/components/MRM";
import { HoldBadge } from "@/components/Hold";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { attentionItems } from "@/lib/attention";
import { GLOSSARY } from "@/lib/labels";
import { relTime } from "@/lib/utils";

// Everything registered. Governance filters from other pages still arrive through the URL;
// the header says what is being filtered so a filtered list never passes for the whole one.

const FILTER_KEYS = ["euSystemRiskClass", "euGpaiTier", "classificationState", "mrmTier", "mrmState"] as const;

const FILTER_LABEL: Record<(typeof FILTER_KEYS)[number], string> = {
  euSystemRiskClass: "EU risk class",
  euGpaiTier: "GPAI tier",
  classificationState: "classification status",
  mrmTier: "model-risk tier",
  mrmState: "model-risk status",
};

const VIEWS = [
  { id: "all", label: "All" },
  { id: "attention", label: "Needs attention" },
  { id: "live", label: "In production" },
  { id: "archived", label: "Archived" },
];

export default function Models() {
  const [params] = useSearchParams();
  const [view, setView] = useTab(VIEWS, "view");
  const q = params.get("q") ?? "";
  const filters = Object.fromEntries(
    FILTER_KEYS.map((k) => [k, params.get(k) ?? ""]).filter(([, v]) => v),
  ) as Record<string, string>;

  const { data, error, loading } = useAsync(
    () => api.models(q, filters),
    [q, filters.euSystemRiskClass, filters.euGpaiTier, filters.classificationState, filters.mrmTier, filters.mrmState],
  );

  const active = Object.entries(filters).map(
    ([k, v]) => `${FILTER_LABEL[k as (typeof FILTER_KEYS)[number]]} is ${v.replace(/_/g, " ")}`,
  );
  if (q) active.unshift(`name contains “${q}”`);

  const models = data?.items ?? [];
  // Attention per model, without reviews or plans: the list shows what the model rollup
  // itself knows; the full list is on Home.
  const flagged = new Map<string, number>();
  for (const it of attentionItems(models, [], [])) flagged.set(it.model, (flagged.get(it.model) ?? 0) + 1);

  const shown = models.filter((m: ModelRollup) =>
    view === "attention"
      ? flagged.has(m.name)
      : view === "live"
        ? !!m.production
        : view === "archived"
          ? m.state === "ARCHIVED"
          : true,
  );

  const counts: Record<string, number> = {
    all: models.length,
    attention: models.filter((m) => flagged.has(m.name)).length,
    live: models.filter((m) => m.production).length,
    archived: models.filter((m) => m.state === "ARCHIVED").length,
  };

  return (
    <div>
      <PageHeader
        title="Models"
        sub={
          active.length ? (
            <span>
              Showing models where {active.join(" and ")}.{" "}
              <Link to="/models" className="font-medium text-brand hover:underline">
                Show all
              </Link>
            </span>
          ) : (
            "Every model registered here, with its production version and governance status."
          )
        }
      />

      <Tabs
        tabs={VIEWS.map((v) => ({
          ...v,
          hint: data ? (
            <span className="rounded-full bg-secondary px-1.5 text-xs tabular-nums text-muted-foreground">
              {counts[v.id]}
            </span>
          ) : undefined,
        }))}
        value={view}
        onChange={setView}
      />

      {loading ? (
        <Loading />
      ) : error ? (
        <ErrorNote error={error} />
      ) : shown.length === 0 ? (
        <Empty>
          {models.length === 0
            ? "No models yet. Publish one through the Model API or the lineage CLI and it will appear here."
            : "No models in this view."}
        </Empty>
      ) : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Model</TableHead>
                <TableHead className="whitespace-nowrap">In production</TableHead>
                <TableHead title={GLOSSARY.euRiskClass}>EU AI Act risk</TableHead>
                <TableHead title={GLOSSARY.modelRisk}>Model risk</TableHead>
                <TableHead className="text-right">Versions</TableHead>
                <TableHead className="text-right">Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((m) => {
                const n = flagged.get(m.name) ?? 0;
                return (
                  <TableRow key={m.id}>
                    <TableCell>
                      <div className="flex flex-wrap items-center gap-2">
                        <Link
                          to={`/models/${encodeURIComponent(m.name)}`}
                          className="whitespace-nowrap font-medium hover:text-brand hover:underline"
                        >
                          {m.name}
                        </Link>
                        {m.state === "ARCHIVED" && <Badge variant="dashed">Archived</Badge>}
                        {m.legalHold && <HoldBadge />}
                        {n > 0 && (
                          <Badge variant="warn">
                            <AlertTriangle className="h-3 w-3" />
                            {n}
                          </Badge>
                        )}
                      </div>
                      <div className="text-xs text-muted-foreground">{m.owner || "No owner recorded"}</div>
                    </TableCell>
                    <TableCell>
                      {m.production ? (
                        <span className="inline-flex items-center gap-2">
                          <span className="h-2 w-2 rounded-full bg-ok" />
                          <span className="font-mono text-[0.8125rem]">{m.production}</span>
                        </span>
                      ) : (
                        <span className="whitespace-nowrap text-muted-foreground">Not released</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <ClassificationCell c={m.classification} />
                    </TableCell>
                    <TableCell>
                      <MRMCell model={m.name} c={m.mrm} />
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{m.versionCount}</TableCell>
                    <TableCell className="text-right text-muted-foreground">{relTime(m.updatedAt)}</TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  );
}
