import * as React from "react";
import { cn } from "@/lib/utils";

/** Immediate hover/focus tooltip; avoids the browser's delayed native `title` UI. */
function Tooltip({
  content,
  children,
  className,
  align = "center",
}: {
  content: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  /** Keep tooltips inside the nearest card when their trigger is near an edge. */
  align?: "start" | "center" | "end";
}) {
  const alignment = {
    start: "left-0",
    center: "left-1/2 -translate-x-1/2",
    end: "right-0",
  }[align];

  return (
    <span className={cn("group/tooltip relative inline-flex", className)}>
      {children}
      <span
        role="tooltip"
        className={cn("pointer-events-none absolute top-full z-30 mt-1 w-max max-w-[min(16rem,calc(100vw-2rem))] border bg-popover px-2 py-1.5 text-left text-xs normal-case leading-relaxed text-popover-foreground opacity-0 shadow-sm transition-none group-hover/tooltip:opacity-100 group-focus-within/tooltip:opacity-100", alignment)}
      >
        {content}
      </span>
    </span>
  );
}

export { Tooltip };
