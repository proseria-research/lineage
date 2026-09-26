import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

// Pills. Status variants (ok / warn / danger) carry meaning; everything else is neutral.
// Sentence case, so a badge reads as a word rather than a code.
const badgeVariants = cva(
  "inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 text-xs font-medium leading-4",
  {
    variants: {
      variant: {
        ok: "border-transparent bg-ok-soft text-ok",
        warn: "border-transparent bg-warn-soft text-warn",
        danger: "border-transparent bg-danger-soft text-danger",
        brand: "border-transparent bg-brand-soft text-brand",
        neutral: "border-transparent bg-secondary text-secondary-foreground",
        // Kept for existing call sites.
        solid: "border-transparent bg-foreground text-background",
        outline: "border-border bg-transparent text-foreground",
        muted: "border-transparent bg-secondary text-muted-foreground",
        dashed: "border-dashed border-border bg-transparent text-muted-foreground",
      },
    },
    defaultVariants: { variant: "neutral" },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };
