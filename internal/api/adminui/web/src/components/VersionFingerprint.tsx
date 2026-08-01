import type { VersionInsight } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty } from "@/components/State";
import { FingerprintMark, RING_ORDER, hasFingerprint } from "@/components/VersionMark";
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

  return (
    <Card className="flex flex-col">
      <CardHeader>
        <CardTitle>Fingerprint</CardTitle>
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
              title={`fingerprint · ${present.length} of 4 hashes reported`}
            />

            {/* Outermost ring first, matching the drawing: topology → shape → dtype → weights. */}
            <div className="grid w-full grid-cols-2 gap-x-4 gap-y-1">
              {RING_ORDER.map((ring) => {
                const reported = !!insight?.hashes?.[ring];
                return (
                  <div
                    key={ring}
                    className="flex items-center gap-1.5"
                    title={insight?.hashes?.[ring] ?? `${ring}: ${NOT_REPORTED}`}
                  >
                    <span
                      className={[
                        "h-1.5 w-1.5 shrink-0 border border-foreground",
                        reported ? "bg-foreground" : "border-dashed bg-transparent opacity-50",
                      ].join(" ")}
                    />
                    <span className={["label-caps truncate", reported ? "" : "opacity-60"].join(" ")}>{ring}</span>
                  </div>
                );
              })}
            </div>

            <p className="text-center text-xs text-muted-foreground">
              {present.length === 4
                ? "All four levels reported — a diff against another version can name exactly what moved."
                : `${present.length} of 4 levels reported. A diff can only narrow to where the hashes reach.`}
            </p>
          </>
        )}
      </CardContent>
    </Card>
  );
}
