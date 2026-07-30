import { Link } from "react-router-dom";
import { api, type Stage } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { relTime } from "@/lib/utils";

const stageOrder: Stage[] = ["production", "staging", "draft", "archived"];

export default function Overview() {
  const { data, error, loading } = useAsync(() => api.overview(), []);
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;

  const stats = [
    { label: "Models", value: data.counts.models },
    { label: "Versions", value: data.counts.versions },
    { label: "Artifacts", value: data.counts.artifacts },
    { label: "Deployments", value: data.counts.deployments },
  ];
  const totalStaged = stageOrder.reduce((n, s) => n + (data.stages[s] || 0), 0) || 1;

  return (
    <div>
      <PageHeader title="Overview" sub="Registry at a glance" />

      <div className="grid grid-cols-2 gap-px border bg-border md:grid-cols-4">
        {stats.map((s) => (
          <div key={s.label} className="bg-card px-4 py-5">
            <div className="label-caps">{s.label}</div>
            <div className="mt-1 font-mono text-3xl font-semibold tabular-nums">{s.value}</div>
          </div>
        ))}
      </div>

      <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Stage distribution</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {stageOrder.map((s) => {
              const n = data.stages[s] || 0;
              return (
                <div key={s} className="flex items-center gap-3">
                  <div className="label-caps w-20 shrink-0">{s}</div>
                  <div className="h-3 flex-1 border">
                    <div
                      className="h-full bg-foreground"
                      style={{ width: `${(n / totalStaged) * 100}%` }}
                    />
                  </div>
                  <div className="w-8 text-right font-mono text-sm tabular-nums">{n}</div>
                </div>
              );
            })}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recent activity</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {data.recent.length === 0 ? (
              <div className="p-4">
                <Empty>No activity yet.</Empty>
              </div>
            ) : (
              <ul className="divide-y">
                {data.recent.map((e) => (
                  <li key={e.id} className="flex items-center justify-between gap-3 px-4 py-2 text-sm">
                    <span className="truncate">
                      <span className="font-mono text-muted-foreground">{e.action}</span>
                      <span className="mx-2 text-muted-foreground">·</span>
                      {e.summary}
                    </span>
                    <span className="label-caps shrink-0">{relTime(e.at)}</span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="mt-6">
        <Link to="/models" className="text-sm underline underline-offset-4 hover:no-underline">
          Browse all models →
        </Link>
      </div>
    </div>
  );
}
