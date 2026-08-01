import { useState } from "react";
import type { VersionInsight } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty } from "@/components/State";
import { PortraitMark, columnsOf, hasPortrait, portraitTone } from "@/components/VersionMark";
import { fmtCount, NOT_REPORTED } from "@/lib/utils";

// The structure section (§12.3): the model's layer stack drawn as a layered network, one
// column per layer with repeats expanded. Full card width, because a left-to-right stack
// squeezed into a square wastes the axis it runs along.
//
// It is independent of the Fingerprint card above it — a version can have hashes and no
// layers, and then this section says so rather than being handed a substitute drawing.

/** One legend row: the channel, and what it is encoding for this version. */
function Chan({ k, v, muted }: { k: string; v: string; muted?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b py-1.5">
      <span className="label-caps shrink-0">{k}</span>
      <span className={["text-right font-mono text-xs", muted ? "italic text-muted-foreground" : ""].join(" ")}>{v}</span>
    </div>
  );
}

export function VersionPortrait({ insight }: { insight: VersionInsight | null }) {
  const [selectedBlock, setSelectedBlock] = useState<number | null>(null);

  if (!hasPortrait(insight)) {
    return (
      <Card className="mb-6">
        <CardHeader>
          <CardTitle>Portrait</CardTitle>
        </CardHeader>
        <CardContent>
          <Empty>
            No layer breakdown reported, so there is no structure to draw. The registry does not open
            model files — a layer breakdown arrives from the SDK at publish or from a scanner.
          </Empty>
        </CardContent>
      </Card>
    );
  }

  const layers = insight!.layers!;
  const selected = selectedBlock == null ? null : layers[selectedBlock] ?? null;
  const columns = columnsOf(layers);
  const deepest = layers.reduce((m, l) => Math.max(m, l.repeatCount || 1), 1);
  const incomplete = layers.filter((l) => l.paramCount == null || !l.shapeSignature).length;
  // The widest layer by output width — the dimension the node counts are drawn from.
  const widest = layers.reduce<{ sig?: string; o: number }>((best, l) => {
    const nums = l.shapeSignature?.match(/\d+/g);
    const o = nums?.length ? Number(nums[nums.length - 1]) : 0;
    return o > best.o ? { sig: l.shapeSignature, o } : best;
  }, { o: 0 });

  const reporter = insight?.reporterName
    ? `${insight.reporterName}${insight.reporterVersion ? " " + insight.reporterVersion : ""}`
    : null;

  return (
    <Card className="mb-6">
      <CardHeader>
        <CardTitle>Portrait</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="border px-4 py-5">
          <PortraitMark
            insight={insight}
            width={880}
            height={260}
            responsive
            title={`portrait · ${columns.length} layers from ${layers.length} block${layers.length === 1 ? "" : "s"}`}
            selectedBlock={selectedBlock}
            onSelectBlock={setSelectedBlock}
          />
        </div>

        <div className="mt-3 flex items-center justify-between gap-4 text-xs text-muted-foreground">
          <span>Choose a column to inspect its block; repeated columns select together.</span>
          {selected && (
            <button type="button" className="shrink-0 underline underline-offset-4 hover:text-foreground" onClick={() => setSelectedBlock(null)}>
              Clear selection
            </button>
          )}
        </div>

        {selected && (
          <div className="mt-3 border px-3 py-2" aria-live="polite">
            <div className="mb-2 flex items-baseline justify-between gap-4">
              <span className={["label-caps", portraitTone(selectedBlock!)].join(" ")}>Selected block {selected.ordinal}</span>
              <span className="truncate font-mono text-xs" title={selected.path}>{selected.path}</span>
            </div>
            <div className="grid grid-cols-1 gap-x-6 sm:grid-cols-2 lg:grid-cols-4">
              <Chan k="Operation" v={selected.opType ?? NOT_REPORTED} muted={!selected.opType} />
              <Chan k="Shape" v={selected.shapeSignature ?? NOT_REPORTED} muted={!selected.shapeSignature} />
              <Chan k="Repeat" v={`×${selected.repeatCount || 1}`} />
              <Chan k="Parameters" v={selected.paramCount == null ? NOT_REPORTED : fmtCount(selected.paramCount)} muted={selected.paramCount == null} />
            </div>
          </div>
        )}

        <div className="mt-5 grid grid-cols-1 gap-x-8 sm:grid-cols-2">
          <Chan
            k="Columns"
            v={`${columns.length} layer${columns.length === 1 ? "" : "s"} · ${layers.length} block${layers.length === 1 ? "" : "s"}${deepest > 1 ? `, deepest ×${deepest}` : ""}`}
          />
          <Chan k="Nodes" v={widest.sig ? `output width · widest ${widest.sig}` : NOT_REPORTED} muted={!widest.sig} />
          <Chan k="Edges" v="schematic — shapes are reported, wiring is not" muted />
          <Chan
            k="Parameters"
            v={insight?.paramCountTotal == null ? NOT_REPORTED : `${fmtCount(insight.paramCountTotal)} total`}
            muted={insight?.paramCountTotal == null}
          />
          {incomplete > 0 && (
            <Chan k="Dashed" v={`${incomplete} block${incomplete === 1 ? "" : "s"} · fact not reported`} muted />
          )}
        </div>

        <p className="mt-3 max-w-prose text-xs text-muted-foreground">
          One column per layer, with <span className="font-mono">repeatCount</span> expanded — six
          transformer layers are six columns. The edges are schematic: producers report block shapes and
          repeat counts, not which unit connects to which, so the wiring shows that layers are adjacent,
          not how they are joined. Deterministic — the same facts draw the same mark
          {reporter ? <>, from what {reporter} reported.</> : "."}
        </p>
      </CardContent>
    </Card>
  );
}
