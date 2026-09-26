import { Link } from "react-router-dom";
import { AlertOctagon, AlertTriangle, CheckCircle2, ChevronRight } from "lucide-react";
import { AREA_LABEL, type AttentionItem } from "@/lib/attention";
import { cn } from "@/lib/utils";

export function AttentionList({ items, emptyText }: { items: AttentionItem[]; emptyText: string }) {
  if (items.length === 0) {
    return (
      <div className="flex items-center gap-3 rounded-lg border bg-card px-5 py-6 text-sm">
        <CheckCircle2 className="h-5 w-5 text-ok" strokeWidth={2} />
        <span>{emptyText}</span>
      </div>
    );
  }
  return (
    <ul className="divide-y overflow-hidden rounded-lg border bg-card">
      {items.map((it) => {
        const Icon = it.severity === "danger" ? AlertOctagon : AlertTriangle;
        return (
          <li key={it.key}>
            <Link
              to={it.to}
              className="group flex items-start gap-3.5 px-5 py-4 transition-colors hover:bg-muted"
            >
              <span
                className={cn(
                  "mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full",
                  it.severity === "danger" ? "bg-danger-soft text-danger" : "bg-warn-soft text-warn",
                )}
              >
                <Icon className="h-4 w-4" strokeWidth={2} />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-medium">{it.title}</span>
                </div>
                <div className="mt-0.5 text-sm">
                  <span className="font-medium text-foreground/90">{it.model}</span>
                  {it.version && <span className="font-mono text-[0.8125rem] text-muted-foreground"> {it.version}</span>}
                  <span className="text-muted-foreground"> · {AREA_LABEL[it.area]}</span>
                </div>
                {it.detail && <p className="mt-1 text-sm text-muted-foreground">{it.detail}</p>}
              </div>
              <span className="mt-1 flex shrink-0 items-center gap-1 text-sm font-medium text-brand opacity-80 group-hover:opacity-100">
                {it.action}
                <ChevronRight className="h-4 w-4" />
              </span>
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
