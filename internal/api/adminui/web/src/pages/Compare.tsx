import { Link, useParams, useSearchParams } from "react-router-dom";
import { api, type Verdict } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import { FingerprintMark, RING_HELP, RING_LABEL, type RingName } from "@/components/VersionMark";
import { TensorInfo } from "@/components/TensorInfo";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
import { VERDICT_LABEL } from "@/components/Review";
import { fmtCount, fmtDeltaBytes, fmtBytesOrUnreported } from "@/lib/utils";

// Version comparison (§11.8): did the model change shape, or just get reweighted? The
// verdict is a lookup over the four-hash ladder, and where the facts do not settle it the
// page says so rather than showing a confident answer the data does not support.

const verdictBlurb: Record<Verdict, string> = {
  identical: "Same architecture and the same weights — a repackage or re-publish.",
  reweighted: "Same architecture and precision, different weights — a fine-tune, continued training, or RL.",
  recast: "Same shape, different precision — a quantization or cast.",
  rescaled: "Same family, different width or depth.",
  rearchitected: "Different topology — new blocks or a new backbone.",
  unknown: "Not enough was reported to say what kind of change this is.",
};

const HASH_LEVELS = ["topology", "shape", "dtype", "weights"] as const;

const FIELD_LABEL: Record<string, string> = {
  paramCountTotal: "Parameters",
  tensorCount: "Tensors",
  diskBytes: "Size on disk",
  weightsBytes: "Weights in memory",
};

export default function Compare() {
  const { model = "" } = useParams();
  const [params] = useSearchParams();
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  const { data, error, loading } = useAsync(() => api.compare(model, from, to), [model, from, to]);

  if (!from || !to) return <ErrorNote error="compare needs ?from= and ?to= version names" />;
  if (loading) return <Loading />;
  if (error) return <ErrorNote error={error} />;
  if (!data) return null;

  const fingerprint = (side: "from" | "to") => ({
    hashes: Object.fromEntries(HASH_LEVELS.map((level) => [level, data.hashes?.[level]?.[side]])),
  });
  const changedLevels = Object.fromEntries(
    HASH_LEVELS.map((level) => [level, data.hashes?.[level]?.changed === true]),
  ) as Partial<Record<RingName, boolean>>;
  const hasChangedLevel = HASH_LEVELS.some((level) => changedLevels[level]);
  const hasFingerprint = HASH_LEVELS.some((level) => data.hashes?.[level]?.from || data.hashes?.[level]?.to);

  return (
    <div>
      <PageHeader
        crumbs={[
          { label: "Models", to: "/models" },
          { label: model, to: `/models/${encodeURIComponent(model)}` },
          { label: "Compare" },
        ]}
        title={
          <span className="flex flex-wrap items-baseline gap-2">
            What changed from
            <Link to={`/models/${model}/versions/${from}`} className="font-mono hover:underline">
              {from}
            </Link>
            to
            <Link to={`/models/${model}/versions/${to}`} className="font-mono hover:underline">
              {to}
            </Link>
          </span>
        }
        sub="Worked out from the fingerprints and facts each version's tools reported. Lineage never opens the model files."
      />

      {/* Verdict */}
      <Card className="mb-6">
        <CardContent className="py-5">
          <div className="flex flex-wrap items-center gap-3">
            <Badge variant={data.verdict === "unknown" ? "warn" : "brand"} className="px-2.5 py-1 text-sm">
              {VERDICT_LABEL[data.verdict]}
            </Badge>
            <span className="text-[0.9375rem]">{verdictBlurb[data.verdict]}</span>
          </div>
          {data.verdict === "unknown" && (
            <p className="mt-2 text-sm text-muted-foreground">
              {data.missing?.length ? <>No {data.missing.join(", ")} hash was reported for one side, so the comparison stops there. </> : null}
              {data.candidates?.length ? (
                <>It is one of: {data.candidates.map((c) => VERDICT_LABEL[c].toLowerCase()).join(" or ")}.</>
              ) : null}
            </p>
          )}
        </CardContent>
      </Card>

      {/* A visual reading of the hash ladder: changed petals stay coloured; matched petals recede. */}
      <Card className="mb-6">
        <CardHeader>
          <CardTitle>Fingerprints side by side</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <div className="border-b px-4 py-4">
            {hasFingerprint ? (
              <div className="flex items-center justify-center gap-8 sm:gap-14">
                <div className="flex flex-col items-center gap-2">
                  <FingerprintMark placeholder
                    insight={fingerprint("from")}
                    size={160}
                    emphasis={hasChangedLevel ? changedLevels : undefined}
                  />
                  <Tooltip content={from}>
                    <span className="max-w-32 truncate font-mono text-xs">{from}</span>
                  </Tooltip>
                </div>
                <span className="text-muted-foreground" aria-hidden="true">→</span>
                <div className="flex flex-col items-center gap-2">
                  <FingerprintMark placeholder
                    insight={fingerprint("to")}
                    size={160}
                    emphasis={hasChangedLevel ? changedLevels : undefined}
                  />
                  <Tooltip content={to} align="end">
                    <span className="max-w-32 truncate font-mono text-xs">{to}</span>
                  </Tooltip>
                </div>
              </div>
            ) : (
              <Empty>No fingerprint hashes were reported for either version.</Empty>
            )}
            {hasFingerprint && (
              <p className="mt-3 text-center text-xs text-muted-foreground">
                {hasChangedLevel ? "Coloured petals changed; grey petals are the same." : "The reported petals match."} Dotted petals were not reported.
              </p>
            )}
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Level</TableHead>
                <TableHead>{from}</TableHead>
                <TableHead>{to}</TableHead>
                <TableHead className="text-right">Result</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {HASH_LEVELS.map((level) => {
                const h = data.hashes?.[level];
                return (
                  <TableRow key={level}>
                    <TableCell className="font-medium" title={RING_HELP[level as RingName]}>{RING_LABEL[level as RingName]}</TableCell>
                    <TableCell className="max-w-[16rem] truncate font-mono text-xs text-muted-foreground">
                      {h?.from || "—"}
                    </TableCell>
                    <TableCell className="max-w-[16rem] truncate font-mono text-xs text-muted-foreground">
                      {h?.to || "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      {/* "not reported" is distinct from "unchanged": one side never told us. */}
                      {!h?.present ? (
                        <Badge variant="dashed">Not reported</Badge>
                      ) : h.changed ? (
                        <Badge variant="warn">Changed</Badge>
                      ) : (
                        <Badge variant="neutral">Same</Badge>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <div className="mb-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* Tensor-level diff: what separates a merged LoRA from a full fine-tune */}
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between gap-2">
              <CardTitle>Tensors</CardTitle>
              <TensorInfo />
            </div>
          </CardHeader>
          <CardContent>
            {!data.tensors ? (
              <Empty>No per-tensor digests reported, so tensor-level change is unknown.</Empty>
            ) : (
              <>
                <div className="grid grid-cols-4 gap-2">
                  {(
                    [
                      ["Unchanged", data.tensors.unchanged],
                      ["Changed", data.tensors.changed],
                      ["Added", data.tensors.added],
                      ["Removed", data.tensors.removed],
                    ] as const
                  ).map(([label, n]) => (
                    <div key={label} className="border p-2">
                      <div className="label-caps">{label}</div>
                      <div className="font-mono text-lg tabular-nums">{n}</div>
                    </div>
                  ))}
                </div>
                {data.tensors.changedPatterns && data.tensors.changedPatterns.length > 0 && (
                  <div className="mt-3">
                    <div className="label-caps mb-1">Changed</div>
                    <ul className="space-y-0.5">
                      {data.tensors.changedPatterns.map((p) => (
                        <li key={p} className="font-mono text-xs">
                          {p}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </>
            )}
          </CardContent>
        </Card>

        {/* Scalars */}
        <Card>
          <CardHeader>
            <CardTitle>Size &amp; parameters</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {!data.params || data.params.length === 0 ? (
              <div className="p-4">
                <Empty>No scalar facts reported on both sides.</Empty>
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Field</TableHead>
                    <TableHead className="text-right">{from}</TableHead>
                    <TableHead className="text-right">{to}</TableHead>
                    <TableHead className="text-right">Delta</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.params.map((p) => {
                    const isBytes = p.field.endsWith("Bytes");
                    const fmt = (n?: number | null) => (isBytes ? fmtBytesOrUnreported(n) : fmtCount(n));
                    return (
                      <TableRow key={p.field}>
                        <TableCell>{FIELD_LABEL[p.field] ?? p.field}</TableCell>
                        <TableCell className="text-right font-mono tabular-nums">{fmt(p.from)}</TableCell>
                        <TableCell className="text-right font-mono tabular-nums">{fmt(p.to)}</TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {p.delta === null || p.delta === undefined
                            ? "—"
                            : isBytes
                              ? fmtDeltaBytes(p.delta)
                              : `${p.delta > 0 ? "+" : ""}${fmtCount(p.delta)}`}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Accuracy traded away */}
      <Card className="mb-6">
        <CardHeader>
          <CardTitle>Evaluations</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {!data.metrics || data.metrics.length === 0 ? (
            <div className="p-4">
              <Empty>No evaluations reported on either version.</Empty>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Suite / metric</TableHead>
                  <TableHead className="text-right">{from}</TableHead>
                  <TableHead className="text-right">{to}</TableHead>
                  <TableHead className="text-right">Delta</TableHead>
                  <TableHead className="text-right">Direction</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.metrics.map((m) => (
                  <TableRow key={`${m.suite}/${m.metric}/${m.split}/${m.harnessVersion}`}>
                    <TableCell>
                      <span className="font-mono text-sm">
                        {m.suite}/{m.metric}
                      </span>
                      <div className="label-caps">
                        {[m.split, m.harnessVersion && `harness ${m.harnessVersion}`].filter(Boolean).join(" · ")}
                      </div>
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{m.from ?? "—"}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{m.to ?? "—"}</TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {m.delta === null || m.delta === undefined ? "—" : `${m.delta > 0 ? "+" : ""}${m.delta.toFixed(4)}`}
                    </TableCell>
                    <TableCell className="text-right">
                      {/* Only results sharing suite/metric/split/harness are comparable;
                          anything else is labelled rather than differenced anyway. */}
                      {m.comparable ? (
                        <Badge variant={m.direction === "worse" ? "solid" : "outline"}>{m.direction}</Badge>
                      ) : (
                        <Tooltip content={m.reason} align="end">
                          <Badge variant="dashed">not comparable</Badge>
                        </Tooltip>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Footprint */}
      {data.footprints && data.footprints.length > 0 && (
        <Card className="mb-6">
          <CardHeader>
            <CardTitle>Memory footprint</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Scenario</TableHead>
                  <TableHead className="text-right">{from}</TableHead>
                  <TableHead className="text-right">{to}</TableHead>
                  <TableHead className="text-right">Delta</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.footprints.map((f) => (
                  <TableRow key={f.scenario}>
                    <TableCell className="font-mono text-sm">
                      {f.scenario}
                      {!f.comparable && (
                        <Badge variant="dashed" className="ml-2">
                          one side only
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {fmtBytesOrUnreported(f.fromTotalBytes)}
                      {f.fromSource && <span className="label-caps ml-1.5">{f.fromSource}</span>}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {fmtBytesOrUnreported(f.toTotalBytes)}
                      {f.toSource && <span className="label-caps ml-1.5">{f.toSource}</span>}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">{fmtDeltaBytes(f.delta)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      {/* What the verdict actually rests on */}
      <p className="text-xs text-muted-foreground">
        Computed from reported facts only; no artifact was read.{" "}
        {data.basis.fromHasInsight ? `${from} reported ${data.basis.fromHashes?.join(", ") || "no"} hashes.` : `${from} has no reported insight.`}{" "}
        {data.basis.toHasInsight ? `${to} reported ${data.basis.toHashes?.join(", ") || "no"} hashes.` : `${to} has no reported insight.`}
      </p>
    </div>
  );
}
