import { ArrowRightLeft, Boxes, GitBranch, Package, Ruler, ShieldCheck } from "lucide-react";
import type { AuditEvent } from "@/lib/api";
import { actionArea, actionLabel, STAGE_LABEL, tidySummary, type ActionArea } from "@/lib/labels";
import { cn, fmtTime, relTime } from "@/lib/utils";

const AREA_ICON: Record<ActionArea, typeof Boxes> = {
  lifecycle: Boxes,
  artifacts: Package,
  lineage: GitBranch,
  facts: Ruler,
  governance: ShieldCheck,
};

/** One audit event as a sentence: what happened, to what, by whom, when. */
export function EventRow({ e, compact = false }: { e: AuditEvent; compact?: boolean }) {
  const stage = e.action === "version.stage_changed" && e.data?.to;
  const Icon = stage ? ArrowRightLeft : AREA_ICON[actionArea(e.action)];
  return (
    <li className={cn("flex items-start gap-3", compact ? "px-4 py-2.5" : "px-5 py-3")}>
      <span
        className={cn(
          "mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full",
          stage && e.data?.to === "production" ? "bg-ok-soft text-ok" : "bg-secondary text-muted-foreground",
        )}
      >
        <Icon className="h-3.5 w-3.5" strokeWidth={2} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-sm">
          <span className="font-medium">
            {stage && e.data?.from
              ? `Moved ${STAGE_LABEL[e.data.from]} → ${STAGE_LABEL[e.data.to as keyof typeof STAGE_LABEL]}`
              : actionLabel(e.action)}
          </span>
          <span className="text-muted-foreground"> · {tidySummary(stage ? e.summary.replace(/\s*→.*$/, "") : e.summary)}</span>
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          {e.actor ? e.actor : "unattributed"}
          {e.data?.reason ? ` — “${e.data.reason}”` : ""}
        </div>
      </div>
      <time className="shrink-0 text-xs text-muted-foreground" dateTime={new Date(e.at).toISOString()} title={fmtTime(e.at)}>
        {relTime(e.at)}
      </time>
    </li>
  );
}
