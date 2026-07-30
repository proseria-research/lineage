import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";
import type { LineageGraph, LineageNode } from "@/lib/api";
import { Empty } from "@/components/State";

// Renders a traversed lineage graph as a monochrome edge list, resolving node ids to labels.
// Version nodes link to their detail page; external refs are shown mono.
export function LineageGraphView({ graph, empty }: { graph?: LineageGraph; empty: string }) {
  if (!graph || graph.edges.length === 0) return <Empty>{empty}</Empty>;

  const byId = new Map<string, LineageNode>();
  for (const n of graph.nodes) if (n.id) byId.set(n.id, n);

  const label = (node?: LineageNode, fallback?: string) => {
    const text = node?.label ?? fallback ?? "—";
    if (node?.type === "model_version" && node.model && node.version) {
      return (
        <Link
          to={`/models/${node.model}/versions/${node.version}`}
          className="font-mono hover:underline underline-offset-4"
        >
          {text}
        </Link>
      );
    }
    return <span className="truncate font-mono text-muted-foreground">{text}</span>;
  };

  return (
    <ul className="space-y-2">
      {graph.edges.map((e) => {
        const src = byId.get(e.srcId);
        const dst = e.dstId ? byId.get(e.dstId) : undefined;
        return (
          <li key={e.id} className="flex items-center gap-2 border px-2.5 py-1.5 text-sm">
            {label(src, e.srcId.slice(0, 8))}
            <span className="label-caps flex shrink-0 items-center gap-1">
              <ArrowRight className="h-3 w-3" strokeWidth={1.5} />
              {e.relation}
            </span>
            {label(dst, e.dstRef)}
          </li>
        );
      })}
    </ul>
  );
}
