import { useParams } from "react-router-dom";
import { api } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { VersionBoard } from "@/components/VersionBoard";
import { HoldAction, HoldNote } from "@/components/Hold";
import { MRMPanel } from "@/components/MRM";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { fmtTime } from "@/lib/utils";

export default function ModelDetail() {
  const { model = "" } = useParams();
  const { data, error, loading, reload } = useAsync(() => api.model(model), [model]);
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

      {/* The marker is state, so it sits in the body above the facts it qualifies — an
          unheld model shows nothing here rather than an empty slot. */}
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

      {/* Labels and the hold action share a row: both are things asserted *about* the model
          rather than about a version. The action is pushed to the far end — labels are read
          left to right, an action is reached for — and the row renders even with no labels,
          so the action has a home either way. */}
      <div className="mb-6 flex flex-wrap items-center gap-2">
        {Object.entries(m.labels ?? {}).map(([k, v]) => (
          <span key={k} className="border px-1.5 py-0.5 font-mono text-xs">
            {k}={v}
          </span>
        ))}
        <div className="ml-auto">
          <HoldAction subject={{ model: m.name }} hold={m.legalHold} onChanged={reload} />
        </div>
      </div>

      {/* The model-risk state is a property of the model — about its production version, or
          its newest when nothing is in production (§20.7). */}
      <MRMPanel c={m.mrm} />

      <div className="label-caps mb-2">Versions</div>
      {data.versions.length === 0 ? (
        <Empty>No versions published.</Empty>
      ) : (
        <VersionBoard model={model} versions={data.versions} />
      )}
    </div>
  );
}
