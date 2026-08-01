import type { LayerBlock, VersionInsight } from "@/lib/api";

// The two procedurally generated marks for one version (§12). They are independent, not
// alternatives — each is drawn from its own input and each is absent on its own terms:
//
//   PortraitMark    <- insight.layers   a layered network, structure
//   FingerprintMark <- insight.hashes   concentric rings, identity
//
// They are visually distinct kinds of object — a wide slate stack, a square ink disc — so
// neither can be mistaken for the other, and a version that has hashes but no layers is
// visibly missing a structure drawing rather than quietly given a substitute (§12.2).
// Everything is 1px currentColor, so both inherit the theme and flip light/dark like the
// rest of the monochrome system. Static by design — the header cube keeps the rotation and
// stays the brand mark.
//
// A version with only an artifact digest draws nothing at all. A digest is a perfectly good
// seed, but a mark made from it says only "these bytes and not other bytes", which the
// Artifacts table already states in text — and an uninterpretable mark teaches the reader to
// skip the section (§12.9).

export function hasPortrait(insight?: VersionInsight | null): boolean {
  return !!insight?.layers && insight.layers.length > 0;
}

export function hasFingerprint(insight?: VersionInsight | null): boolean {
  return !!insight?.hashes && RING_ORDER.some((r) => insight.hashes?.[r]);
}

// ---- §12.5 determinism. No Math.random, no time, no layout-dependent values: the same
// facts draw the same pixels on every machine and every render. ----

function fnv1a32(s: string): number {
  let h = 0x811c9dc5;
  const bytes = new TextEncoder().encode(s);
  for (let i = 0; i < bytes.length; i++) {
    h ^= bytes[i];
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

/** Successive xorshift32 words, seeded by fnv1a32 of the source string. */
function stream(seed: string): () => number {
  let x = fnv1a32(seed) || 0x9e3779b9;
  return () => {
    x ^= x << 13;
    x >>>= 0;
    x ^= x >>> 17;
    x ^= x << 5;
    x >>>= 0;
    return x;
  };
}

// ---- §12.3 Portrait: a layered network, left to right ----

const MAX_NODES = 6; // above this the edge bundle turns to grey mush at 128px
const MAX_COLUMNS = 20;

/** Output width of a block: the last dim of "[768,3072]". Null when unparseable. */
function outWidth(sig?: string): number | null {
  if (!sig) return null;
  const nums = sig.match(/\d+/g);
  if (!nums?.length) return null;
  return Number(nums[nums.length - 1]);
}

/** One drawn column. A block with repeatCount n contributes n of them. */
type Column = { nodes: number; block: number; dashed: boolean };

/**
 * Expand blocks into columns. `repeatCount` becomes literal repetition — six transformer
 * layers are six columns — which is the one thing a stack diagram says better than a bar.
 */
export function columnsOf(layers: LayerBlock[]): Column[] {
  const outs = layers.map((l) => outWidth(l.shapeSignature));
  const outMax = Math.max(2, ...outs.filter((o): o is number => o != null));

  const cols: Column[] = [];
  layers.forEach((l, i) => {
    const o = outs[i];
    // Node count is the layer's output width, log-scaled. Narrow heads are drawn at their
    // true width, so a [768,2] classifier visibly pinches to two nodes.
    let nodes: number;
    if (o == null) nodes = 2;
    else if (o <= MAX_NODES) nodes = Math.max(1, o);
    else nodes = Math.max(2, Math.min(MAX_NODES, Math.round(2 + (MAX_NODES - 2) * (Math.log(o) / Math.log(outMax)))));

    const dashed = o == null || l.paramCount == null;
    const n = Math.max(l.repeatCount || 1, 1);
    for (let k = 0; k < n && cols.length < MAX_COLUMNS; k++) cols.push({ nodes, block: i, dashed });
  });
  return cols;
}

function Portrait({ layers, w, h, reduced }: { layers: LayerBlock[]; w: number; h: number; reduced: boolean }) {
  // The stack runs left to right, so the box is padded asymmetrically: tight on the sides to
  // give the columns room, generous top and bottom so the widest layer is not flush.
  const padX = Math.max(2, w * 0.045);
  const padY = Math.max(2, h * 0.12);
  const W = w - padX * 2;
  const H = h - padY * 2;
  const cols = columnsOf(layers);
  if (!cols.length) return null;

  const maxNodes = Math.max(...cols.map((c) => c.nodes));
  const dx = cols.length > 1 ? W / (cols.length - 1) : 0;
  const cx = (i: number) => padX + (cols.length > 1 ? i * dx : W / 2);

  // A column's vertical extent is its node count relative to the widest layer, so the
  // silhouette of the stack reads as the model narrowing and widening.
  const ys = cols.map((c) => {
    const extent = H * (c.nodes / maxNodes);
    const top = padY + (H - extent) / 2;
    if (c.nodes === 1) return [padY + H / 2];
    return Array.from({ length: c.nodes }, (_, k) => top + (extent * k) / (c.nodes - 1));
  });

  const out: React.ReactElement[] = [];

  // Reduced: columns as ticks, no nodes and no edges — at 20px both are mud (§12.6).
  if (reduced) {
    cols.forEach((c, i) => {
      const extent = H * (c.nodes / maxNodes);
      const x = Math.round(cx(i)) + 0.5;
      out.push(
        <line
          key={`t${i}`}
          x1={x}
          y1={padY + (H - extent) / 2}
          x2={x}
          y2={padY + (H + extent) / 2}
          stroke="currentColor"
          strokeWidth={1}
          shapeRendering="crispEdges"
        />,
      );
    });
    return <>{out}</>;
  }

  // Edges first, so nodes sit on top of the bundle. Drawn faint: at full weight the
  // connections swallow the nodes and the stack stops reading as layers.
  for (let i = 0; i < cols.length - 1; i++) {
    const dashed = cols[i].dashed || cols[i + 1].dashed;
    ys[i].forEach((y1, a) => {
      ys[i + 1].forEach((y2, b) => {
        out.push(
          <line
            key={`e${i}_${a}_${b}`}
            x1={cx(i)}
            y1={y1}
            x2={cx(i + 1)}
            y2={y2}
            stroke="currentColor"
            strokeWidth={1}
            strokeOpacity={dashed ? 0.09 : 0.18}
          />,
        );
      });
    });
  }

  const r = Math.max(1, Math.min(4.5, h / 46));
  cols.forEach((c, i) => {
    ys[i].forEach((y, k) => {
      out.push(
        <circle
          key={`n${i}_${k}`}
          cx={cx(i)}
          cy={y}
          r={r}
          stroke="currentColor"
          strokeWidth={1}
          // A missing fact is a visible state, never a guessed size (§12.2).
          strokeDasharray={c.dashed ? "1.5 1.5" : undefined}
          className="fill-card"
        />,
      );
    });
  });

  return <>{out}</>;
}

// ---- §12.4 Fingerprint ----

export const RING_ORDER = ["topology", "shape", "dtype", "weights"] as const;
export type RingName = (typeof RING_ORDER)[number];

const RING_R = [44, 34, 24, 14]; // radii in a 96px box

/**
 * One low-chroma hue per level, so a ring can be named without counting inward. Colour is
 * never the only signal: the radius is fixed and the roll-call under the disc repeats each
 * level as text, so the mark still reads with colour vision loss or in print (§12.7).
 */
export const RING_TONE: Record<RingName, { text: string; bg: string; border: string }> = {
  topology: { text: "text-fp-topology", bg: "bg-fp-topology", border: "border-fp-topology" },
  shape: { text: "text-fp-shape", bg: "bg-fp-shape", border: "border-fp-shape" },
  dtype: { text: "text-fp-dtype", bg: "bg-fp-dtype", border: "border-fp-dtype" },
  weights: { text: "text-fp-weights", bg: "bg-fp-weights", border: "border-fp-weights" },
};

function arcPath(cx: number, cy: number, r: number, a0: number, a1: number): string {
  const p0x = cx + r * Math.cos(a0);
  const p0y = cy + r * Math.sin(a0);
  const p1x = cx + r * Math.cos(a1);
  const p1y = cy + r * Math.sin(a1);
  return `M ${p0x.toFixed(2)} ${p0y.toFixed(2)} A ${r.toFixed(2)} ${r.toFixed(2)} 0 ${a1 - a0 > Math.PI ? 1 : 0} 1 ${p1x.toFixed(2)} ${p1y.toFixed(2)}`;
}

function Fingerprint({
  hashes,
  size,
  segments,
  emphasis,
}: {
  hashes: Partial<Record<RingName, string>>;
  size: number;
  segments: number;
  emphasis?: Partial<Record<RingName, boolean>>;
}) {
  const k = size / 96;
  const cx = size / 2;
  const cy = size / 2;
  const out: React.ReactElement[] = [];

  RING_ORDER.forEach((name, i) => {
    const r = RING_R[i] * k;
    const h = hashes[name];

    // Absent is a dotted circle in the muted tone: present-but-different and
    // absent-entirely must not look alike (§12.4).
    if (!h) {
      out.push(
        <circle
          key={name}
          cx={cx}
          cy={cy}
          r={r}
          className="text-muted-foreground"
          stroke="currentColor"
          strokeWidth={1}
          fill="none"
          strokeDasharray="1 3"
        />,
      );
      return;
    }

    const next = stream(h);
    const rot = (7.5 * i * Math.PI) / 180; // so rings never align into spokes
    const step = (2 * Math.PI) / segments;
    const gap = (2 * Math.PI) / 180;
    let word = next();
    let bit = 0;

    for (let seg = 0; seg < segments; seg++) {
      if (bit >= 30) {
        word = next();
        bit = 0;
      }
      const on = (word >>> bit) & 1;
      bit++;
      if (!on) continue;
      out.push(
        <path
          key={`${name}${seg}`}
          d={arcPath(cx, cy, r, rot + seg * step + gap / 2, rot + (seg + 1) * step - gap / 2)}
          // In a delta pair, rings that did not change drop to muted so the ones that did
          // carry their colour alone (§12.4).
          className={emphasis && !emphasis[name] ? "text-muted-foreground" : RING_TONE[name].text}
          stroke="currentColor"
          strokeWidth={1}
          fill="none"
        />,
      );
    }
  });

  return <>{out}</>;
}

// ---- the marks ----

/**
 * The structure drawing: a wide, left-to-right layered network. Renders nothing unless a
 * producer reported `insight.layers`.
 */
export function PortraitMark({
  insight,
  width = 880,
  height = 260,
  responsive,
  reduced,
  className,
  title,
}: {
  insight?: VersionInsight | null;
  width?: number;
  height?: number;
  /** Scale to the container width, keeping the viewBox aspect. */
  responsive?: boolean;
  /** Below 24px the detail channels collide into grey, so the reduction is mandatory (§12.6). */
  reduced?: boolean;
  className?: string;
  title?: string;
}) {
  if (!hasPortrait(insight)) return null;
  const small = reduced ?? Math.min(width, height) < 24;

  // The portrait is the console's one coloured surface (§12.3): a drawn network needs to
  // separate from the 1px furniture around it. The fingerprint stays ink — structure is
  // coloured, identity is not, which is one more way the two marks do not blend. At tick
  // size the colour would read as a stray highlight, so it goes with the detail.
  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      fill="none"
      aria-hidden="true"
      className={[small ? undefined : "text-portrait", className].filter(Boolean).join(" ") || undefined}
      {...(responsive
        ? { preserveAspectRatio: "xMidYMid meet", style: { display: "block", width: "100%", height: "auto" } }
        : { width, height, style: { display: "block", flex: "none" } })}
    >
      {title && <title>{title}</title>}
      <Portrait layers={insight!.layers!} w={width} h={height} reduced={small} />
    </svg>
  );
}

/**
 * The identity mark: four concentric rings, one per fingerprint hash. Renders nothing
 * unless at least one hash was reported.
 */
export function FingerprintMark({
  insight,
  size = 160,
  reduced,
  emphasis,
  className,
  title,
}: {
  insight?: VersionInsight | null;
  size?: number;
  reduced?: boolean;
  /** Rings mapped false are drawn muted, for a side-by-side delta. */
  emphasis?: Partial<Record<RingName, boolean>>;
  className?: string;
  title?: string;
}) {
  if (!hasFingerprint(insight)) return null;
  const small = reduced ?? size < 24;

  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      fill="none"
      aria-hidden="true"
      className={className}
      style={{ display: "block", flex: "none" }}
    >
      {title && <title>{title}</title>}
      <Fingerprint
        hashes={insight!.hashes as Partial<Record<RingName, string>>}
        size={size}
        segments={small ? 8 : 24}
        emphasis={emphasis}
      />
    </svg>
  );
}
