import * as React from "react";
import { useSearchParams } from "react-router-dom";
import { cn } from "@/lib/utils";

export interface TabDef {
  id: string;
  label: string;
  /** A count or status shown beside the label, e.g. how many items need attention. */
  hint?: React.ReactNode;
}

/**
 * Tabs whose selection lives in the URL (`?tab=`), so a tab can be linked to and survives a
 * reload. The first tab is the default and is left out of the URL.
 */
export function useTab(tabs: TabDef[], param = "tab") {
  const [params, setParams] = useSearchParams();
  const current = tabs.find((t) => t.id === params.get(param))?.id ?? tabs[0].id;
  const select = (id: string) => {
    const next = new URLSearchParams(params);
    if (id === tabs[0].id) next.delete(param);
    else next.set(param, id);
    setParams(next, { replace: true });
  };
  return [current, select] as const;
}

export function Tabs({
  tabs,
  value,
  onChange,
  className,
}: {
  tabs: TabDef[];
  value: string;
  onChange: (id: string) => void;
  className?: string;
}) {
  return (
    <div role="tablist" className={cn("mb-6 flex gap-1 overflow-x-auto overflow-y-hidden shadow-[inset_0_-1px_0_var(--color-border)]", className)}>
      {tabs.map((t) => {
        const active = t.id === value;
        return (
          <button
            key={t.id}
            role="tab"
            aria-selected={active}
            onClick={() => onChange(t.id)}
            className={cn(
              "flex shrink-0 items-center gap-2 border-b-2 px-3 py-2.5 text-sm font-medium transition-colors",
              active
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {t.label}
            {t.hint}
          </button>
        );
      })}
    </div>
  );
}
