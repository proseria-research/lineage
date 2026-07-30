import { useState } from "react";
import { ArrowUpRight, Archive, Undo2, ChevronRight } from "lucide-react";
import { api, type Stage } from "@/lib/api";
import { Button } from "@/components/ui/button";

const meta: Record<Stage, { label: string; icon: typeof ArrowUpRight; promote?: boolean }> = {
  production: { label: "Promote to production", icon: ArrowUpRight, promote: true },
  staging: { label: "Send to staging", icon: ChevronRight },
  draft: { label: "Return to draft", icon: Undo2 },
  archived: { label: "Archive", icon: Archive },
};

// Human-approver stage controls (§06.4). Renders only the legal next stages and calls the
// same core :transition the Model API uses. Promoting into production auto-demotes the
// incumbent (singleton), so we confirm first.
export function StageActions({
  model,
  version,
  targets,
  onDone,
}: {
  model: string;
  version: string;
  targets: Stage[];
  onDone: () => void;
}) {
  const [busy, setBusy] = useState<Stage | null>(null);
  const [error, setError] = useState("");

  async function go(to: Stage) {
    if (to === "production" && !confirm(`Promote ${model}@${version} to production?\nThis archives the current production version.`)) {
      return;
    }
    setBusy(to);
    setError("");
    try {
      await api.transition(model, version, to);
      onDone();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(null);
    }
  }

  if (targets.length === 0) return null;

  return (
    <div className="flex flex-col items-end gap-1.5">
      <div className="flex flex-wrap justify-end gap-2">
        {targets.map((t) => {
          const { label, icon: Icon, promote } = meta[t];
          return (
            <Button
              key={t}
              size="sm"
              variant={promote ? "default" : "outline"}
              disabled={busy !== null}
              onClick={() => go(t)}
            >
              <Icon className="h-3.5 w-3.5" strokeWidth={1.5} />
              {busy === t ? "Working…" : label}
            </Button>
          );
        })}
      </div>
      {error && <div className="text-xs text-destructive">{error}</div>}
    </div>
  );
}
