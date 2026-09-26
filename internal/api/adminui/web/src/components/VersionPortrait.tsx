import { useState, type ReactNode } from "react";
import { Info } from "lucide-react";
import type { VersionInsight } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tooltip } from "@/components/ui/tooltip";
import { Empty } from "@/components/State";
import { Dimensions } from "@/components/Dimensions";
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

/** A compact fact in the selected-layer summary. */
function LayerFact({ label, value, muted }: { label: string; value: ReactNode; muted?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="label-caps text-muted-foreground">{label}</div>
      <div className={[(muted ? "italic text-muted-foreground" : "font-mono"), "mt-1 min-w-0 text-xs"].join(" ")}>{value}</div>
    </div>
  );
}

function PortraitStat({
  label,
  value,
  help,
  tooltipAlign,
}: {
  label: string;
  value: string;
  help: string;
  tooltipAlign: "start" | "center" | "end";
}) {
  return (
    <div className="px-3 py-2 first:border-r last:border-l">
      <div className="flex items-center gap-1 label-caps text-muted-foreground">
        {label}
        <Tooltip content={help} align={tooltipAlign}>
          <span className="inline-flex cursor-help" aria-label={`${label}: ${help}`} role="note">
            <Info size={11} strokeWidth={1.5} aria-hidden="true" />
          </span>
        </Tooltip>
      </div>
      <div className="mt-1 font-mono text-sm tabular-nums">{value}</div>
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
  const reporter = insight?.reporterName
    ? `${insight.reporterName}${insight.reporterVersion ? " " + insight.reporterVersion : ""}`
    : null;
  const portraitHelp = `One column represents one layer; repeated blocks are expanded into separate columns. Lines show adjacent layers, not the model's actual wiring. The same reported facts always draw the same portrait${reporter ? `, as reported by ${reporter}` : ""}.`;

  return (
    <Card className="mb-6">
      <CardHeader>
        <div className="flex items-center justify-between gap-2">
          <CardTitle>Portrait</CardTitle>
          <Tooltip content={portraitHelp} className="shrink-0" align="end">
            <span className="cursor-help text-muted-foreground" aria-label={portraitHelp} role="note">
              <Info size={13} strokeWidth={1.5} aria-hidden="true" />
            </span>
          </Tooltip>
        </div>
      </CardHeader>
      <CardContent>
        <div className="border px-4 py-5 rounded-lg">
          <PortraitMark
            insight={insight}
            width={880}
            height={260}
            responsive
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
          <div className="mt-3 border px-3 py-2.5 rounded-md" aria-live="polite">
            <div className="flex items-start justify-between gap-4">
              <span className={["label-caps", portraitTone(selectedBlock!)].join(" ")}>Block {selected.ordinal}</span>
              <Tooltip content={selected.path} className="max-w-[55%]" align="end">
                <span className="truncate font-mono text-xs text-muted-foreground">{selected.path}</span>
              </Tooltip>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-x-6 gap-y-3 lg:grid-cols-4">
              <LayerFact label="Type" value={selected.opType ?? NOT_REPORTED} muted={!selected.opType} />
              <LayerFact label="Output" value={<Dimensions value={selected.shapeSignature} />} muted={!selected.shapeSignature} />
              <LayerFact label="Repeats" value={`×${selected.repeatCount || 1}`} />
              <LayerFact label="Parameters" value={selected.paramCount == null ? NOT_REPORTED : fmtCount(selected.paramCount)} muted={selected.paramCount == null} />
            </div>
          </div>
        )}

        <div className="mt-5 grid grid-cols-3 border">
          <PortraitStat label="Layers" value={String(columns.length)} help="The total number of layers drawn. A repeated block contributes one layer for each repetition." tooltipAlign="start" />
          <PortraitStat label="Blocks" value={String(layers.length)} help="The number of distinct building blocks reported for this model. One block can be repeated across several layers." tooltipAlign="center" />
          <PortraitStat label="Max repeat" value={`×${deepest}`} help="The longest run of the same building block in the model." tooltipAlign="end" />
        </div>

        <div className="mt-3 grid grid-cols-1 gap-x-8 sm:grid-cols-2">
          <Chan k="Connections" v="adjacent layers · schematic" muted />
          <Chan
            k="Total parameters"
            v={insight?.paramCountTotal == null ? NOT_REPORTED : fmtCount(insight.paramCountTotal)}
            muted={insight?.paramCountTotal == null}
          />
          {incomplete > 0 && (
            <Chan k="Dotted layers" v={`${incomplete} incomplete block${incomplete === 1 ? "" : "s"}`} muted />
          )}
        </div>

      </CardContent>
    </Card>
  );
}
