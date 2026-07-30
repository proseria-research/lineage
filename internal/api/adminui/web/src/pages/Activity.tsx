import { useEffect, useState } from "react";
import { api, type AuditEvent } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { fmtTime } from "@/lib/utils";

export default function Activity() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [token, setToken] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);

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

  return (
    <div>
      <PageHeader title="Activity" sub="Every state transition, audited" />
      {error && <ErrorNote error={error} />}
      {events.length === 0 && loading ? (
        <Loading />
      ) : events.length === 0 ? (
        <Empty>No activity recorded.</Empty>
      ) : (
        <Card>
          <ul className="divide-y">
            {events.map((e) => (
              <li key={e.id} className="grid grid-cols-[9rem_1fr_auto] items-center gap-3 px-4 py-2 text-sm">
                <span className="font-mono text-xs text-muted-foreground">{e.action}</span>
                <span className="truncate">{e.summary}</span>
                <span className="label-caps shrink-0">{fmtTime(e.at)}</span>
              </li>
            ))}
          </ul>
        </Card>
      )}
      {!done && events.length > 0 && (
        <div className="mt-4">
          <Button variant="outline" size="sm" disabled={loading} onClick={() => load(token)}>
            {loading ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
    </div>
  );
}
