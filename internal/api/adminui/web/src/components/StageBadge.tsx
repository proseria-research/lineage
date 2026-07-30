import { Badge } from "@/components/ui/badge";
import type { Stage } from "@/lib/api";

const variant: Record<Stage, "solid" | "outline" | "muted" | "dashed"> = {
  production: "solid",
  staging: "outline",
  draft: "muted",
  archived: "dashed",
};

export function StageBadge({ stage }: { stage?: Stage }) {
  if (!stage) return <span className="text-muted-foreground">—</span>;
  return <Badge variant={variant[stage] ?? "muted"}>{stage}</Badge>;
}
