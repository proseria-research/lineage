import { useParams } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { VersionBoard } from "@/components/VersionBoard";
import { HoldNote } from "@/components/Hold";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { fmtTime } from "@/lib/utils";

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

      {/* A held model is marked, and the marker says what the hold blocks — §19.8. */}
      {m.legalHold ? (
        <div className="mb-6">
          <HoldNote hold={m.legalHold} />
        </div>
      ) : null}

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
        <VersionBoard model={model} versions={data.versions} />
      )}
    </div>
  );
}
