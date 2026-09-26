import { Badge } from "@/components/ui/badge";
import type { Stage } from "@/lib/api";
import { STAGE_HELP, STAGE_LABEL } from "@/lib/labels";

const variant: Record<Stage, "ok" | "brand" | "neutral" | "dashed"> = {
  production: "ok",
  staging: "brand",
  draft: "neutral",
  archived: "dashed",
};

export function StageBadge({ stage }: { stage?: Stage }) {
  if (!stage) return <span className="text-muted-foreground">—</span>;
  return (
    <Badge variant={variant[stage] ?? "neutral"} title={STAGE_HELP[stage]}>
      {stage === "production" && <span className="h-1.5 w-1.5 rounded-full bg-current" />}
      {STAGE_LABEL[stage]}
    </Badge>
  );
}
