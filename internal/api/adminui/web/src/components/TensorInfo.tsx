import { Info } from "lucide-react";
import { Tooltip } from "@/components/ui/tooltip";

const TENSOR_HELP = "A tensor is a named collection of numbers that stores part of a model's learned values. The console uses tensors to describe a model's internal data and compare what changed between versions.";

/** A shared, plain-language explanation used wherever tensor data is shown. */
export function TensorInfo() {
  return (
    <Tooltip content={TENSOR_HELP} align="end">
      <span className="inline-flex cursor-help text-muted-foreground" aria-label="What tensors are" role="note">
        <Info size={13} strokeWidth={1.5} aria-hidden="true" />
      </span>
    </Tooltip>
  );
}
