import type { LineageEdge, LineageGraph, LineageNode } from "@/lib/api";
import { Empty } from "@/components/State";

type Side = "upstream" | "downstream";

interface Walk {
  levels: Map<number, LineageNode[]>;
  edgeIds: Set<string>;
}

interface PositionedNode {
  key: string;
  node: LineageNode;
  side: Side | "root";
  depth: number;
  x: number;
  y: number;
}

const NODE_W = 236;
const NODE_H = 84;
const ROOT_W = 252;
const ROOT_H = 92;
const COLUMN_GAP = 120;
const ROW_GAP = 22;
const PADDING_X = 24;
const PADDING_Y = 28;

function nodeKey(node: LineageNode) {
  return node.type === "model_version" ? `v:${node.id}` : `ext:${node.ref}`;
}

function srcKey(edge: LineageEdge) {
  return `v:${edge.srcId}`;
}

function dstKey(edge: LineageEdge) {
  return edge.dstId ? `v:${edge.dstId}` : `ext:${edge.dstRef}`;
}

function walkGraph(graph: LineageGraph, byKey: Map<string, LineageNode>, side: Side): Walk {
  const levels = new Map<number, LineageNode[]>();
  const edgeIds = new Set<string>();
  const visited = new Set([graph.root]);
  const queue = [{ key: graph.root, depth: 0 }];

  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const edge of graph.edges) {
      const from = side === "upstream" ? srcKey(edge) : dstKey(edge);
      if (from !== current.key) continue;

      const nextKey = side === "upstream" ? dstKey(edge) : srcKey(edge);
      if (nextKey === graph.root) continue;
      const next = byKey.get(nextKey);
      if (!next) continue;

      edgeIds.add(edge.id);
      if (visited.has(nextKey)) continue;
      visited.add(nextKey);
      const depth = current.depth + 1;
      levels.set(depth, [...(levels.get(depth) ?? []), next]);
      queue.push({ key: nextKey, depth });
    }
  }

  for (const nodes of levels.values()) nodes.sort((a, b) => a.label.localeCompare(b.label));
  return { levels, edgeIds };
}

function maxDepth(walk: Walk) {
  return Math.max(0, ...walk.levels.keys());
}

function maxRows(walk: Walk) {
  return Math.max(0, ...[...walk.levels.values()].map((nodes) => nodes.length));
}

function truncate(text: string, limit = 32) {
  if (text.length <= limit) return text;
  return `${text.slice(0, limit - 1)}…`;
}

function kindFor(node: LineageNode, relation?: string) {
  if (node.type === "model_version") return "Model version";
  if (relation === "trained_on") return "Dataset";
  if (relation === "produced_by") return "Pipeline run";
  if (relation === "deployed_as") return "Deployment";
  return "External reference";
}

// Each connector is labelled in the direction it is drawn — left to right, source to what was
// made from it — so "A → derived → B" reads as "B was derived from A".
const FLOW_LABEL: Record<string, string> = {
  derived_from: "derived",
  trained_on: "trained",
  produced_by: "produced",
  deployed_as: "deployed as",
};

const STAGE_TONE: Record<string, { fill: string; text: string; label: string }> = {
  production: { fill: "var(--ok-soft)", text: "var(--ok)", label: "Production" },
  staging: { fill: "var(--brand-soft)", text: "var(--brand)", label: "Staging" },
  draft: { fill: "var(--secondary)", text: "var(--muted-foreground)", label: "Draft" },
  archived: { fill: "var(--secondary)", text: "var(--muted-foreground)", label: "Archived" },
};

/** An external ref reads best as its last path segment, with the scheme and host as context. */
function splitRef(ref: string): [string, string] {
  const trimmed = ref.replace(/\/+$/, "");
  const cut = trimmed.lastIndexOf("/");
  if (cut <= 0) return [trimmed, ""];
  return [trimmed.slice(cut + 1), trimmed.slice(0, cut + 1)];
}

function relationForNode(graph: LineageGraph, key: string, side: Side) {
  const edge = graph.edges.find((candidate) =>
    side === "upstream" ? dstKey(candidate) === key : srcKey(candidate) === key,
  );
  return edge?.relation;
}

function nodeMeta(node: LineageNode, relation?: string) {
  if (node.type === "model_version") return node.stage || "version";
  return kindFor(node, relation);
}

function nodeHref(node: LineageNode) {
  if (node.type !== "model_version" || !node.model || !node.version) return undefined;
  return `/models/${encodeURIComponent(node.model)}/versions/${encodeURIComponent(node.version)}`;
}

function edgePath(from: PositionedNode, to: PositionedNode) {
  const fromW = from.side === "root" ? ROOT_W : NODE_W;
  const fromX = from.x + fromW;
  const fromY = from.y + (from.side === "root" ? ROOT_H : NODE_H) / 2;
  const toX = to.x;
  const toY = to.y + (to.side === "root" ? ROOT_H : NODE_H) / 2;
  const bend = Math.max(48, (toX - fromX) * 0.42);
  return `M ${fromX} ${fromY} C ${fromX + bend} ${fromY} ${toX - bend} ${toY} ${toX} ${toY}`;
}

function layout(graph: LineageGraph) {
  const byKey = new Map(graph.nodes.map((node) => [nodeKey(node), node]));
  const root = byKey.get(graph.root);
  if (!root) return null;

  const upstream = walkGraph(graph, byKey, "upstream");
  const downstream = walkGraph(graph, byKey, "downstream");
  const upstreamDepth = maxDepth(upstream);
  const downstreamDepth = maxDepth(downstream);
  const rows = Math.max(1, maxRows(upstream), maxRows(downstream));
  const height = Math.max(ROOT_H + PADDING_Y * 2, PADDING_Y * 2 + rows * NODE_H + Math.max(0, rows - 1) * ROW_GAP);
  const rootX = PADDING_X + upstreamDepth * (NODE_W + COLUMN_GAP);
  const width =
    PADDING_X * 2 +
    upstreamDepth * (NODE_W + COLUMN_GAP) +
    ROOT_W +
    downstreamDepth * (NODE_W + COLUMN_GAP);
  const rootPosition: PositionedNode = {
    key: graph.root,
    node: root,
    side: "root",
    depth: 0,
    x: rootX,
    y: (height - ROOT_H) / 2,
  };

  const positioned: PositionedNode[] = [rootPosition];
  const place = (walk: Walk, side: Side) => {
    for (const [depth, nodes] of walk.levels) {
      const columnHeight = nodes.length * NODE_H + Math.max(0, nodes.length - 1) * ROW_GAP;
      const top = (height - columnHeight) / 2;
      nodes.forEach((node, index) => {
        const x =
          side === "upstream"
            ? rootX - depth * (NODE_W + COLUMN_GAP)
            : rootX + ROOT_W + COLUMN_GAP + (depth - 1) * (NODE_W + COLUMN_GAP);
        positioned.push({
          key: nodeKey(node),
          node,
          side,
          depth,
          x,
          y: top + index * (NODE_H + ROW_GAP),
        });
      });
    }
  };
  place(upstream, "upstream");
  place(downstream, "downstream");

  return { root, upstream, downstream, positioned, width, height };
}

function MindmapNode({ item, graph }: { item: PositionedNode; graph: LineageGraph }) {
  const isRoot = item.side === "root";
  const width = isRoot ? ROOT_W : NODE_W;
  const height = isRoot ? ROOT_H : NODE_H;
  const relation = item.side === "root" ? undefined : relationForNode(graph, item.key, item.side);
  const href = nodeHref(item.node);
  const n = item.node;
  const x = item.x + 16;
  const version = n.type === "model_version";
  const [primary, secondary] = version ? [n.model ?? n.label, n.version ?? ""] : splitRef(n.ref ?? n.label);
  const stage = version && n.stage ? STAGE_TONE[n.stage] : undefined;
  const content = (
    <g className={`lineage-map__node-group${isRoot ? " lineage-map__subject" : ""}`}>
      <rect className="lineage-map__node" x={item.x} y={item.y} width={width} height={height} rx={10} />
      <text className="lineage-map__kind" x={x} y={item.y + 22}>
        {isRoot ? "This version" : kindFor(n, relation)}
      </text>
      {stage && (
        <g>
          <rect x={item.x + width - 16 - stage.label.length * 6.4 - 16} y={item.y + 10} width={stage.label.length * 6.4 + 16} height={18} rx={9} style={{ fill: stage.fill }} />
          <text x={item.x + width - 16 - (stage.label.length * 6.4 + 16) / 2} y={item.y + 23} textAnchor="middle" className="lineage-map__stage" style={{ fill: stage.text }}>
            {stage.label}
          </text>
        </g>
      )}
      <text className={isRoot ? "lineage-map__subject-name" : "lineage-map__name"} x={x} y={item.y + (isRoot ? 50 : 47)}>
        {truncate(primary, isRoot ? 24 : 26)}
      </text>
      <text className="lineage-map__meta" x={x} y={item.y + (isRoot ? 72 : 67)}>
        {truncate(secondary, 34)}
      </text>
    </g>
  );

  return href && !isRoot ? (
    <a href={href} className="lineage-map__node-link" aria-label={`Open ${n.label}`}>
      {content}
    </a>
  ) : (
    content
  );
}

// A dynamic version of the public site's provenance mindmap. The API graph is laid out
// by traversal depth: ancestry to the left, impact to the right, and the selected version
// at the centre. The native details list keeps dense or assistive-tech reading practical.
export function LineageGraphView({ graph, empty }: { graph?: LineageGraph; empty: string }) {
  if (!graph || graph.edges.length === 0) return <Empty>{empty}</Empty>;
  const map = layout(graph);
  if (!map) return <Empty>{empty}</Empty>;

  const positions = new Map(map.positioned.map((item) => [`${item.side}:${item.key}`, item]));
  const rootPosition = positions.get(`root:${graph.root}`)!;
  const drawnEdges: { edge: LineageEdge; side: Side; from: PositionedNode; to: PositionedNode }[] = [];

  for (const edge of graph.edges) {
    if (map.upstream.edgeIds.has(edge.id)) {
      const ancestor = positions.get(`upstream:${dstKey(edge)}`);
      const descendant = srcKey(edge) === graph.root ? rootPosition : positions.get(`upstream:${srcKey(edge)}`);
      if (ancestor && descendant) drawnEdges.push({ edge, side: "upstream", from: ancestor, to: descendant });
    }
    if (map.downstream.edgeIds.has(edge.id)) {
      const ancestor = dstKey(edge) === graph.root ? rootPosition : positions.get(`downstream:${dstKey(edge)}`);
      const descendant = positions.get(`downstream:${srcKey(edge)}`);
      if (ancestor && descendant) drawnEdges.push({ edge, side: "downstream", from: ancestor, to: descendant });
    }
  }

  const description = `${map.upstream.edgeIds.size} upstream and ${map.downstream.edgeIds.size} downstream relationships for ${map.root.label}.`;

  return (
    <div>
      <div className="lineage-map__legend" aria-hidden="true">
        <span>← Built from</span>
        <span>Used by →</span>
      </div>
      <figure className="lineage-map">
        <svg
          viewBox={`0 0 ${map.width} ${map.height}`}
          width={map.width}
          height={map.height}
          role="img"
          aria-label={`Lineage of ${map.root.label}. ${description}`}
        >
          <defs>
            <marker id="lineage-map-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">
              <path className="lineage-map__arrow" d="M 0 0 L 10 5 L 0 10 z" />
            </marker>
          </defs>

          {drawnEdges.map(({ edge, side, from, to }) => {
            const d = edgePath(from, to);
            const fromW = from.side === "root" ? ROOT_W : NODE_W;
            const fromH = from.side === "root" ? ROOT_H : NODE_H;
            const toH = to.side === "root" ? ROOT_H : NODE_H;
            const labelX = (from.x + fromW + to.x) / 2;
            const labelY = (from.y + fromH / 2 + to.y + toH / 2) / 2;
            const text = FLOW_LABEL[edge.relation] ?? edge.relation.replace(/_/g, " ");
            const pillW = text.length * 6.6 + 18;
            return (
              <g key={`${side}:${edge.id}`} className="lineage-map__edge">
                <path className="lineage-map__hit" d={d} />
                <path className="lineage-map__branch" d={d} markerEnd="url(#lineage-map-arrow)" />
                <rect className="lineage-map__pill" x={labelX - pillW / 2} y={labelY - 10} width={pillW} height={20} rx={10} />
                <text className="lineage-map__relation" x={labelX} y={labelY + 4} textAnchor="middle">
                  {text}
                </text>
              </g>
            );
          })}

          {map.positioned.filter((item) => item.side !== "root").map((item) => (
            <MindmapNode key={`${item.side}:${item.key}`} item={item} graph={graph} />
          ))}
          <MindmapNode item={rootPosition} graph={graph} />
        </svg>
      </figure>

      <details className="lineage-map__list">
        <summary>View relationships as a list</summary>
        <ul>
          {drawnEdges.map(({ edge, side, from, to }) => (
            <li key={`list:${side}:${edge.id}`}>
              <span>{from.node.label}</span>
              <span className="label-caps">{edge.relation.replace(/_/g, " ")}</span>
              <span>{to.node.label}</span>
            </li>
          ))}
        </ul>
      </details>
    </div>
  );
}
