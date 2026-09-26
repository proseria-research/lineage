import { RING_LABEL } from "@/components/VersionMark";
import type { ReactNode } from "react";
import type { Evaluation, FactSource, FieldSource, Footprint, VersionInsight } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import { TensorInfo } from "@/components/TensorInfo";
import { Empty } from "@/components/State";
import { fmtBytesOrUnreported, fmtCount, NOT_REPORTED } from "@/lib/utils";

// Model insights (§11.8). The registry derives none of these values, so every one is shown
// with where it came from, and a fact nobody submitted reads as "not reported" — never as a
// zero, and never as a blank a reader might mistake for one.

const sourceVariant: Record<FactSource, "solid" | "outline" | "muted" | "dashed"> = {
  measured: "solid", // observed on real hardware
  derived: "outline", // computed from the model itself
  declared: "dashed", // a claim: attributed, not verified
};

const SOURCE_LABEL: Record<string, string> = {
  measured: "Measured",
  derived: "Computed",
  declared: "Stated",
};

const SOURCE_HELP: Record<string, string> = {
  measured: "Measured by running the model, for example an evaluation.",
  derived: "Computed from the model files by the tool that reported it.",
  declared: "Stated by a person or tool without being checked.",
};

const PARAM_METHOD: Record<string, string> = {
  from_tensors: "counted from the weights",
  from_config: "read from the config",
  declared: "as stated",
};

function SourceTag({ source, help }: { source?: FactSource | string; help?: string }) {
  if (!source) return null;
  const v = sourceVariant[source as FactSource] ?? "muted";
  const badge = <Badge variant={v}>{SOURCE_LABEL[source] ?? source}</Badge>;
  return <Tooltip content={help ?? SOURCE_HELP[source] ?? source} align="end">{badge}</Tooltip>;
}

/** One labelled fact. `value` is null/undefined when nobody reported it. */
function Fact({
  label,
  value,
  attribution,
  mono,
}: {
  label: ReactNode;
  value?: string | null;
  attribution?: FieldSource;
  mono?: boolean;
}) {
  const reported = value !== null && value !== undefined && value !== NOT_REPORTED;
  return (
    <div className="border-b py-2 last:border-b-0">
      <div className="label-caps">{label}</div>
      <div className="mt-0.5 flex items-baseline justify-between gap-2">
        <span
          className={[
            "text-sm",
            mono ? "font-mono" : "",
            reported ? "" : "italic text-muted-foreground",
          ].join(" ")}
        >
          {reported ? value : NOT_REPORTED}
        </span>
        {reported && attribution && (
          <span className="flex shrink-0 items-center gap-1.5">
            <SourceTag
              source={attribution.source}
              help={
                attribution.reporter
                  ? `reported by ${attribution.reporter}${attribution.reporterVersion ? " " + attribution.reporterVersion : ""}`
                  : undefined
              }
            />
          </span>
        )}
      </div>
    </div>
  );
}

export function InsightPanel({
  insight,
  footprints,
  evaluations,
}: {
  insight: VersionInsight | null;
  footprints: Footprint[];
  evaluations: Evaluation[];
}) {
  if (!insight && footprints.length === 0 && evaluations.length === 0) {
    return (
      <Card className="mb-6">
        <CardHeader>
          <CardTitle>Composition</CardTitle>
        </CardHeader>
        <CardContent>
          <Empty>
            No producer has reported on this version. The registry does not inspect model files — facts
            arrive from the SDK at publish, a scanner, an eval harness, or a load test.
          </Empty>
        </CardContent>
      </Card>
    );
  }

  const fs = (field: string): FieldSource | undefined => insight?.fieldSources?.[field];
  const framework = insight?.framework?.name
    ? `${insight.framework.name}${insight.framework.version ? " " + insight.framework.version : ""}`
    : null;
  const precision = insight?.dtypeDominant
    ? insight.quantMethod
      ? `${insight.dtypeDominant} · ${insight.quantMethod}`
      : insight.dtypeDominant
    : null;

  return (
    <>
      <Card className="mb-6">
        <CardHeader>
          <CardTitle>Composition</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 gap-x-8 sm:grid-cols-2 lg:grid-cols-3">
            <Fact
              label="Parameters"
              value={
                insight?.paramCountTotal === null || insight?.paramCountTotal === undefined
                  ? null
                  : `${fmtCount(insight.paramCountTotal)}${insight.paramCountMethod ? `, ${PARAM_METHOD[insight.paramCountMethod] ?? insight.paramCountMethod.replace(/_/g, " ")}` : ""}`
              }
              attribution={fs("paramCountTotal")}
            />
            <Fact label="Framework" value={framework} attribution={fs("framework")} />
            <Fact label="Precision" value={precision} attribution={fs("dtypeDominant")} />
            <Fact
              label={<span className="flex items-center gap-1">Tensors <TensorInfo /></span>}
              value={insight?.tensorCount === null || insight?.tensorCount === undefined ? null : fmtCount(insight.tensorCount)}
              attribution={fs("tensorCount")}
            />
            <Fact
              label="Size on disk"
              value={insight?.diskBytes === null || insight?.diskBytes === undefined ? null : fmtBytesOrUnreported(insight.diskBytes)}
              attribution={fs("diskBytes")}
            />
            <Fact
              label="Weights in memory"
              value={
                insight?.weightsBytes === null || insight?.weightsBytes === undefined
                  ? null
                  : fmtBytesOrUnreported(insight.weightsBytes)
              }
              attribution={fs("weightsBytes")}
            />
          </div>

          {insight && (
            <div className="mt-4 border-t pt-3">
              <div className="label-caps mb-1.5">Fingerprint</div>
              <div className="grid grid-cols-2 gap-x-6 gap-y-1 sm:grid-cols-4">
                {(["topology", "shape", "dtype", "weights"] as const).map((level) => (
                  <div key={level}>
                    <div className="label-caps">{RING_LABEL[level]}</div>
                    <Tooltip content={insight.hashes?.[level] ?? NOT_REPORTED}>
                      <span className={[
                        "truncate font-mono text-xs",
                        insight.hashes?.[level] ? "" : "italic text-muted-foreground",
                      ].join(" ")}>
                        {insight.hashes?.[level] ?? NOT_REPORTED}
                      </span>
                    </Tooltip>
                  </div>
                ))}
              </div>
              {!insight.hashes?.weights && (
                <p className="mt-2 text-xs text-muted-foreground">
                  Without a weights hash a diff cannot separate an untouched republish from a fine-tune.
                </p>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      {insight?.layers && insight.layers.length > 0 && (
        <Card className="mb-6">
          <CardHeader>
            <CardTitle>Layer breakdown</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Path</TableHead>
                  <TableHead>Op</TableHead>
                  <TableHead className="text-right">Repeats</TableHead>
                  <TableHead>Shape</TableHead>
                  <TableHead className="text-right">Params (each)</TableHead>
                  <TableHead className="text-right">Params (total)</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {insight.layers.map((l) => (
                  <TableRow key={l.ordinal}>
                    <TableCell className="font-mono text-xs">{l.path}</TableCell>
                    <TableCell className="label-caps">{l.opType || "—"}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {l.repeatCount > 1 ? `×${l.repeatCount}` : "—"}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">{l.shapeSignature || "—"}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{fmtCount(l.paramCount)}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {l.paramCount === null || l.paramCount === undefined
                        ? NOT_REPORTED
                        : fmtCount(l.paramCount * l.repeatCount)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      <div className="mb-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Memory footprint</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {footprints.length === 0 ? (
              <div className="p-4">
                <Empty>No footprint reported.</Empty>
              </div>
            ) : (
              <ul className="divide-y">
                {footprints.map((f) => (
                  <li key={f.id} className="px-4 py-2.5">
                    <div className="flex items-baseline justify-between gap-2">
                      <span className="font-mono text-sm">{f.scenario}</span>
                      <span className="flex items-center gap-2">
                        <span className="font-mono text-sm tabular-nums">{fmtBytesOrUnreported(f.totalBytes)}</span>
                        <SourceTag source={f.source} />
                      </span>
                    </div>
                    {/* An estimate is only interpretable alongside its assumptions, so the
                        basis is always shown next to the number, never hidden. */}
                    <div className="label-caps mt-0.5">
                      {[
                        f.deviceClass,
                        f.batch != null ? `batch ${f.batch}` : null,
                        f.seqLen != null ? `seq ${f.seqLen}` : null,
                        ...Object.entries(f.basis ?? {}).map(([k, v]) => `${k} ${String(v)}`),
                      ]
                        .filter(Boolean)
                        .join(" · ") || "no stated basis"}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Evaluations</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {evaluations.length === 0 ? (
              <div className="p-4">
                <Empty>No evaluation reported.</Empty>
              </div>
            ) : (
              <ul className="divide-y">
                {evaluations.map((e) => (
                  <li key={e.id} className="px-4 py-2.5">
                    <div className="flex items-baseline justify-between gap-2">
                      <span className="text-sm">
                        <span className="font-mono">{e.suite}</span>
                        <span className="mx-1 text-muted-foreground">/</span>
                        <span className="font-mono">{e.metric}</span>
                        {e.split && <span className="ml-1.5 text-muted-foreground">{e.split}</span>}
                      </span>
                      <span className="flex items-center gap-2">
                        <span className="font-mono text-sm tabular-nums">{e.value}</span>
                        <SourceTag source={e.source} />
                      </span>
                    </div>
                    <div className="label-caps mt-0.5">
                      {[
                        e.harnessName ? `${e.harnessName}${e.harnessVersion ? " " + e.harnessVersion : ""}` : null,
                        e.nSamples != null ? `n=${e.nSamples}` : null,
                        e.higherIsBetter ? "higher is better" : "lower is better",
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}
