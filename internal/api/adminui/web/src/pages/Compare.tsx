import { Link, useParams, useSearchParams } from "react-router-dom";
import { api, type Verdict } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import { PageHeader, Loading, ErrorNote, Empty } from "@/components/State";
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
  unknown: "The submitted facts do not determine a verdict.",
};

const HASH_LEVELS = ["topology", "shape", "dtype", "weights"] as const;

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

  return (
    <div>
      <PageHeader
        title={
          <span className="flex items-baseline gap-2">
            <Link to={`/models/${model}`} className="text-muted-foreground hover:underline">
              {model}
            </Link>
            <span className="text-muted-foreground">/</span>
            <Link to={`/models/${model}/versions/${from}`} className="font-mono hover:underline">
              {from}
            </Link>
            <span className="text-muted-foreground">→</span>
            <Link to={`/models/${model}/versions/${to}`} className="font-mono hover:underline">
              {to}
            </Link>
          </span>
        }
      />

      {/* Verdict */}
      <Card className="mb-6">
        <CardContent className="py-4">
          <div className="flex flex-wrap items-baseline gap-3">
            <Badge variant={data.verdict === "unknown" ? "dashed" : "solid"} className="text-sm">
              {data.verdict}
            </Badge>
            <span className="text-sm">{verdictBlurb[data.verdict]}</span>
          </div>
          {data.verdict === "unknown" && (
            <p className="mt-2 text-sm text-muted-foreground">
              {data.missing?.length ? (
                <>
                  Missing: <span className="font-mono">{data.missing.join(", ")}</span>.{" "}
                </>
              ) : null}
              {data.candidates?.length ? <>Narrowed to {data.candidates.join(" or ")}.</> : null}
            </p>
          )}
        </CardContent>
      </Card>

      {/* Hash ladder */}
      <Card className="mb-6">
        <CardHeader>
          <CardTitle>Fingerprint</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
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
                    <TableCell className="label-caps">{level}</TableCell>
                    <TableCell className="max-w-[16rem] truncate font-mono text-xs text-muted-foreground">
                      {h?.from || "—"}
                    </TableCell>
                    <TableCell className="max-w-[16rem] truncate font-mono text-xs text-muted-foreground">
                      {h?.to || "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      {/* "not reported" is distinct from "unchanged": one side never told us. */}
                      {!h?.present ? (
                        <Badge variant="dashed">not reported</Badge>
                      ) : h.changed ? (
                        <Badge variant="solid">changed</Badge>
                      ) : (
                        <Badge variant="muted">same</Badge>
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
            <CardTitle>Tensors</CardTitle>
          </CardHeader>
          <CardContent>
            {!data.tensors ? (
              <Empty>No per-tensor digests reported, so tensor-level change is unknown.</Empty>
            ) : (
              <>
                <div className="grid grid-cols-4 gap-2">
                  {(
                    [
                      ["unchanged", data.tensors.unchanged],
                      ["changed", data.tensors.changed],
                      ["added", data.tensors.added],
                      ["removed", data.tensors.removed],
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
                        <TableCell className="font-mono text-xs">{p.field}</TableCell>
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
