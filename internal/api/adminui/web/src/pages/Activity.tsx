import { useEffect, useState } from "react";
import { api, type AuditEvent } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Tabs } from "@/components/ui/tabs";
import { EventRow } from "@/components/ActivityFeed";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { actionArea, type ActionArea } from "@/lib/labels";

const FILTERS: { id: "all" | ActionArea; label: string }[] = [
  { id: "all", label: "Everything" },
  { id: "lifecycle", label: "Releases" },
  { id: "artifacts", label: "Artifacts" },
  { id: "lineage", label: "Lineage" },
  { id: "facts", label: "Model facts" },
  { id: "governance", label: "Governance" },
];

const dayLabel = (ms: number) => {
  const d = new Date(ms);
  const today = new Date();
  const yesterday = new Date(Date.now() - 86_400_000);
  if (d.toDateString() === today.toDateString()) return "Today";
  if (d.toDateString() === yesterday.toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long", year: "numeric" });
};

export default function Activity() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [token, setToken] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);
  const [filter, setFilter] = useState<"all" | ActionArea>("all");

  async function load(t: string) {
    setLoading(true);
    try {
      const r = await api.activity(t);
      setEvents((prev) => (t ? [...prev, ...r.items] : r.items));
      setToken(r.nextPageToken);
      setDone(!r.nextPageToken);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const shown = events.filter((e) => filter === "all" || actionArea(e.action) === filter);
  const days: { label: string; items: AuditEvent[] }[] = [];
  for (const e of shown) {
    const label = dayLabel(e.at);
    const last = days[days.length - 1];
    if (last && last.label === label) last.items.push(e);
    else days.push({ label, items: [e] });
  }

  return (
    <div>
      <PageHeader
        title="Activity"
        sub="Every change anyone made, newest first. Each entry is written in the same transaction as the change, so this list is complete."
      />
      <Tabs tabs={FILTERS} value={filter} onChange={(id) => setFilter(id as typeof filter)} />
      {error && <ErrorNote error={error} />}
      {events.length === 0 && loading ? (
        <Loading />
      ) : shown.length === 0 ? (
        <Empty>{events.length === 0 ? "Nothing has happened yet." : "Nothing of this kind in the loaded history."}</Empty>
      ) : (
        <div className="space-y-6">
          {days.map((d) => (
            <section key={d.label}>
              <h2 className="mb-2 text-sm font-semibold text-muted-foreground">{d.label}</h2>
              <ul className="divide-y overflow-hidden rounded-lg border bg-card">
                {d.items.map((e) => (
                  <EventRow key={e.id} e={e} />
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
      {!done && events.length > 0 && (
        <div className="mt-6">
          <Button variant="outline" size="sm" disabled={loading} onClick={() => load(token)}>
            {loading ? "Loading…" : "Load older activity"}
          </Button>
        </div>
      )}
    </div>
  );
}
