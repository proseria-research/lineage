import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

// Monochrome badges: differentiation comes from fill / outline / dashed, never color.
const badgeVariants = cva(
  "inline-flex items-center border px-1.5 py-0.5 text-[0.6875rem] font-medium uppercase tracking-wider leading-none",
  {
    variants: {
      variant: {
        solid: "bg-foreground text-background border-foreground",
        outline: "bg-transparent text-foreground border-foreground",
        muted: "bg-transparent text-muted-foreground border-border",
        dashed: "bg-transparent text-muted-foreground border-border border-dashed",
      },
    },
    defaultVariants: { variant: "outline" },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };
