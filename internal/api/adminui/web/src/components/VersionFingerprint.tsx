import type { VersionInsight } from "@/lib/api";
import { Info } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tooltip } from "@/components/ui/tooltip";
import { Empty } from "@/components/State";
import { FingerprintMark, RING_ORDER, RING_TONE, hasFingerprint } from "@/components/VersionMark";
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
          <CardTitle>Fingerprint</CardTitle>
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
            No fingerprint hashes reported. Without them a diff cannot separate an untouched republish
            from a fine-tune.
          </Empty>
        ) : (
          <>
            <FingerprintMark
              insight={insight}
              size={168}
            />

            {/* Outermost ring first, matching the drawing: topology → shape → dtype → weights.
                Each marker carries its ring's colour, which is what turns the hues in the disc
                from decoration into a legend. An absent level stays grey and dashed — colour
                means "reported", so absence cannot borrow one. */}
            <div className="grid w-full grid-cols-2 gap-x-4 gap-y-1">
              {RING_ORDER.map((ring) => {
                const reported = !!insight?.hashes?.[ring];
                const tone = RING_TONE[ring];
                return (
                  <Tooltip key={ring} content={insight?.hashes?.[ring] ?? `${ring}: ${NOT_REPORTED}`}>
                    <span className="flex items-center gap-1.5">
                      <span
                        className={[
                          "h-2 w-2 shrink-0 border",
                          reported
                            ? `${tone.bg} ${tone.border}`
                            : "border-dashed border-muted-foreground bg-transparent opacity-50",
                        ].join(" ")}
                      />
                      <span className={["label-caps truncate", reported ? "" : "opacity-60"].join(" ")}>{ring}</span>
                    </span>
                  </Tooltip>
                );
              })}
            </div>

          </>
        )}
      </CardContent>
    </Card>
  );
}
