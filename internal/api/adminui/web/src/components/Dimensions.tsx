import { Fragment } from "react";
import { cn, NOT_REPORTED } from "@/lib/utils";

/**
 * Renders a reported shape signature as labelled dimension cells. The final dimension is
 * the output width used by the portrait; earlier dimensions are intentionally called axes,
 * because a producer does not report their semantic role.
 */
export function Dimensions({ value, className }: { value?: string; className?: string }) {
  const dimensions = value?.match(/\d+/g);
  if (!dimensions?.length) return <span className={cn("italic text-muted-foreground", className)}>{NOT_REPORTED}</span>;
  const output = Number(dimensions[dimensions.length - 1]).toLocaleString();

  return (
    <span
      className={cn("inline-flex max-w-full flex-wrap items-end gap-1.5 align-middle", className)}
      aria-label={`Shape: ${dimensions.map((dimension) => Number(dimension).toLocaleString()).join(" by ")}. Output width: ${output}.`}
    >
      {dimensions.map((dimension, index) => {
        const isOutput = index === dimensions.length - 1;
        const formatted = Number(dimension).toLocaleString();
        const symbol = isOutput ? "d_out" : `d_${index + 1}`;
        const help = isOutput
          ? `${symbol} is ${formatted}. Each item leaving this part of the model is represented by ${formatted} numbers. The portrait uses this value to show the layer's width.`
          : `${symbol} is ${formatted}. It is another size reported for this part of the model. The source did not say what it represents, so it cannot be named more precisely.`;

        return (
          <Fragment key={`${dimension}-${index}`}>
            {index > 0 && <span className="mb-0.5 text-muted-foreground">×</span>}
            <span className="group/dimension relative inline-flex cursor-help items-baseline gap-1 border bg-muted px-1.5 py-1 leading-none rounded-md" aria-label={help}>
              <span className="font-mono text-[10px] text-muted-foreground">
                <i>d</i><sub>{isOutput ? "out" : index + 1}</sub>
              </span>
              <span className="tabular-nums">{formatted}</span>
              <span
                role="tooltip"
                className={cn(
                  "pointer-events-none absolute top-full z-20 mt-1 w-56 max-w-[min(14rem,calc(100vw-2rem))] border bg-popover px-2 py-1.5 text-left text-xs leading-relaxed text-popover-foreground opacity-0 shadow-sm transition-none group-hover/dimension:opacity-100",
                  index === 0 ? "left-0" : "right-0",
                )}
              >
                {help}
              </span>
            </span>
          </Fragment>
        );
      })}
    </span>
  );
}
