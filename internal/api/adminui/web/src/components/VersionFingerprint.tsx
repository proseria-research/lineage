import type { VersionInsight } from "@/lib/api";
import { Info } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tooltip } from "@/components/ui/tooltip";
import { Empty } from "@/components/State";
import { FingerprintMark, RING_HELP, RING_LABEL, RING_ORDER, RING_TONE, hasFingerprint } from "@/components/VersionMark";
import { NOT_REPORTED } from "@/lib/utils";

// The identity section (§12.4): four concentric rings, one per fingerprint hash, sized to
// sit square beside Lifecycle at the top of the version page. It is deliberately compact —
// this is the glance, and the full hashes are in the Composition panel further down.
//
// The ring roll-call under the disc is the honest part: a hash nobody reported is a hollow
// square and a dotted ring, so "present but different" and "absent entirely" never look
// alike. Two versions of one model can be compared by eye from this card alone.

export function VersionFingerprint({ insight }: { insight: VersionInsight | null }) {
  const present = RING_ORDER.filter((r) => insight?.hashes?.[r]);

  // What the coverage means for a diff. It is an aside, not a fact about this version, so it
  // lives behind an info icon in the header rather than spending a line of the card.
  const reach =
    present.length === 4
      ? "All four levels reported — a diff against another version can name exactly what moved."
      : `${present.length} of 4 levels reported. A diff can only narrow to where the hashes reach.`;

  return (
    <Card className="flex flex-col">
      <CardHeader>
        <div className="flex items-center justify-between gap-2">
          <div>
            <CardTitle>Fingerprint</CardTitle>
            <p className="text-xs text-muted-foreground">Identity at four depths, outermost first</p>
          </div>
          {present.length > 0 && (
            <Tooltip content={reach} className="shrink-0" align="end">
              <span className="cursor-help text-muted-foreground" aria-label={reach} role="note">
                <Info size={13} strokeWidth={1.5} aria-hidden="true" />
              </span>
            </Tooltip>
          )}
        </div>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col items-center justify-center gap-4 py-5">
        {!hasFingerprint(insight) ? (
          <Empty>
            No fingerprint reported. Without one, Lineage can't tell an untouched re-publish from a
            fine-tune.
          </Empty>
        ) : (
          <>
            <FingerprintMark
              insight={insight}
              size={168}
            />

            {/* Outermost ring first, matching the drawing. An absent level stays grey and
                dashed — colour means "reported", so absence cannot borrow one. */}
            <ul className="w-full space-y-1.5">
              {RING_ORDER.map((ring) => {
                const h = insight?.hashes?.[ring];
                return (
                  <li key={ring}>
                    <Tooltip className="flex w-full" content={h ? `${RING_HELP[ring]} ${h}` : `${RING_HELP[ring]} Not reported.`}>
                      <span className="flex w-full items-center gap-2 text-sm">
                        <span
                          className={
                            h
                              ? `h-2.5 w-2.5 shrink-0 rounded-full ${RING_TONE[ring].bg}`
                              : "h-2.5 w-2.5 shrink-0 rounded-full border border-dashed border-muted-foreground"
                          }
                        />
                        <span className={h ? "" : "text-muted-foreground"}>{RING_LABEL[ring]}</span>
                        <span className="ml-auto truncate font-mono text-xs text-muted-foreground">
                          {h ? h.replace(/^sha256:/, "").slice(0, 8) : NOT_REPORTED}
                        </span>
                      </span>
                    </Tooltip>
                  </li>
                );
              })}
            </ul>
          </>
        )}
      </CardContent>
    </Card>
  );
}
