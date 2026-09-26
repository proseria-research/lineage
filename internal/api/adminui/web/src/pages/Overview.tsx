import { Link } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { attentionItems } from "@/lib/attention";
import { AttentionList } from "@/components/Attention";
import { EventRow } from "@/components/ActivityFeed";
import { StageBadge } from "@/components/StageBadge";
import { PageHeader, Loading, ErrorNote, SectionHeader } from "@/components/State";
import { relTime } from "@/lib/utils";

// Home answers three questions in order: what needs me, what is live, what just happened.

export default function Overview() {
  const { data, error, loading } = useAsync(
    () =>
      Promise.all([api.overview(), api.models(), api.reviews("open"), api.changePlans()]).then(
        ([overview, models, reviews, plans]) => ({ overview, models: models.items, reviews: reviews.items, plans }),
      ),
    [],
  );
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;

  const { overview, models } = data;
  const items = attentionItems(models, data.reviews, data.plans.items);
  const danger = items.filter((i) => i.severity === "danger").length;
  const live = models.filter((m) => m.production).sort((a, b) => b.updatedAt - a.updatedAt);
  const notLive = models.length - live.length;

  const summary =
    items.length === 0
      ? `All ${models.length} models are in order.`
      : `${items.length} ${items.length === 1 ? "thing needs" : "things need"} attention${
          danger ? `, ${danger} of them ${danger === 1 ? "a problem" : "problems"}` : ""
        }.`;

  return (
    <div>
      <PageHeader title="Home" sub={summary} />

      <div className="grid grid-cols-1 gap-8 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <section>
          <SectionHeader
            title="Needs attention"
            sub="Collected from risk classification, reviews, model risk and change plans. Nothing here blocks a release."
          />
          <AttentionList items={items} emptyText="Nothing needs attention. Every assessment is current." />
        </section>

        <aside className="space-y-8">
          <section>
            <SectionHeader
              title="In production"
              sub={`${live.length} of ${models.length} models${notLive ? `; ${notLive} not released yet` : ""}`}
            />
            <ul className="divide-y overflow-hidden rounded-lg border bg-card">
              {live.length === 0 && <li className="px-4 py-5 text-sm text-muted-foreground">No model is in production yet.</li>}
              {live.map((m) => (
                <li key={m.id}>
                  <Link
                    to={`/models/${encodeURIComponent(m.name)}`}
                    className="flex items-center justify-between gap-3 px-4 py-3 hover:bg-muted"
                  >
                    <div className="min-w-0">
                      <div className="truncate font-medium">{m.name}</div>
                      <div className="text-xs text-muted-foreground">updated {relTime(m.updatedAt)}</div>
                    </div>
                    <span className="flex items-center gap-2">
                      <span className="font-mono text-sm">{m.production}</span>
                      <StageBadge stage="production" />
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>

          <section>
            <SectionHeader title="Registry" />
            <dl className="grid grid-cols-2 gap-3">
              {[
                ["Models", overview.counts.models],
                ["Versions", overview.counts.versions],
                ["Artifacts", overview.counts.artifacts],
                ["Deployments", overview.counts.deployments],
              ].map(([k, v]) => (
                <div key={k} className="rounded-lg border bg-card px-4 py-3">
                  <dt className="text-xs text-muted-foreground">{k}</dt>
                  <dd className="mt-0.5 text-xl font-semibold tabular-nums">{v}</dd>
                </div>
              ))}
            </dl>
          </section>
        </aside>
      </div>

      <section className="mt-10">
        <SectionHeader
          title="Recent activity"
          right={
            <Link to="/activity" className="text-sm font-medium text-brand hover:underline">
              All activity
            </Link>
          }
        />
        <ul className="divide-y overflow-hidden rounded-lg border bg-card">
          {overview.recent.length === 0 && <li className="px-5 py-5 text-sm text-muted-foreground">Nothing has happened yet.</li>}
          {overview.recent.slice(0, 8).map((e) => (
            <EventRow key={e.id} e={e} />
          ))}
        </ul>
      </section>
    </div>
  );
}
