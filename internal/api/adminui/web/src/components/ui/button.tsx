import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50 border cursor-pointer select-none",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground border-primary hover:opacity-90",
        outline: "bg-card text-foreground border-input hover:bg-accent",
        ghost: "border-transparent hover:bg-accent hover:text-accent-foreground",
      },
      size: {
        default: "h-9 px-3.5",
        sm: "h-8 px-2.5 text-[0.8125rem]",
        icon: "h-9 w-9",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button ref={ref} className={cn(buttonVariants({ variant, size, className }))} {...props} />
  ),
);
Button.displayName = "Button";

export { Button, buttonVariants };
