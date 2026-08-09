import { useState } from "react";
import { Link } from "react-router-dom";
import { api, type ReviewItem, type ReviewStatus, type Verdict } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";
import { FingerprintMark, type RingName } from "@/components/VersionMark";
import { ReviewDialog } from "@/components/ReviewDialog";
import { Empty } from "@/components/State";
import { relTime } from "@/lib/utils";

// The Art. 25 review queue (§17.7). A derivation on a governed model may have made whoever
// made it the model's *provider* in law, with the whole documentation and conformity burden
// attached. The registry surfaces the ones worth a look and records what a human concluded.
//
// Three things this view will not do:
//
//  - It will not say whether a change is substantial in law. Art. 3(23) turns on the system,
//    its context and its documentation, none of which is stored here (§17.3).
//  - It will not block anything. There is no action on this page that stops a publish, a
//    promotion or a delete — it is a list, not a gate (§17.1).
//  - It will not hide an `unknown`. "We cannot tell what changed" is the case that most wants
//    human eyes; dropping it would make a missing weights hash look like a clean bill of
//    health (§17.4).

const HASH_LEVELS = ["topology", "shape", "dtype", "weights"] as const;

const VERDICT_TEXT: Record<Verdict, string> = {
  identical: "Bytes match — a repackage.",
  reweighted: "Weights changed; topology and shapes did not.",
  recast: "Precision changed only.",
  rescaled: "Width or depth changed.",
  rearchitected: "New blocks or backbone.",
  unknown: "The hashes do not reach — nobody can say what changed.",
};

export function VerdictBadge({ v }: { v: Verdict }) {
  // `unknown` is dashed, not absent and not solid: it is a real position in the queue, and it
  // is emphatically not a verdict of "nothing changed".
  return (
    <Tooltip content={VERDICT_TEXT[v]}>
      <Badge variant={v === "unknown" ? "dashed" : "solid"}>{v}</Badge>
    </Tooltip>
  );
}

/** One derivation: what changed, what was declared, and whether anyone has looked. */
function ReviewRow({ it, onReview }: { it: ReviewItem; onReview: (it: ReviewItem) => void }) {
  const side = (which: "from" | "to") => ({
    hashes: Object.fromEntries(HASH_LEVELS.map((l) => [l, it.hashes?.[l]?.[which]])),
  });
  const changed = Object.fromEntries(
    HASH_LEVELS.map((l) => [l, it.hashes?.[l]?.changed === true]),
  ) as Partial<Record<RingName, boolean>>;
  const anyChanged = HASH_LEVELS.some((l) => changed[l]);
  const anyHash = HASH_LEVELS.some((l) => it.hashes?.[l]?.from || it.hashes?.[l]?.to);

  const parent = it.derivedFrom
    ? `${it.derivedFrom.model}@${it.derivedFrom.version}`
    : it.derivedFromRef;
  // A verdict recorded against different facts than the ones on screen now means a producer
  // submitted a hash after the review. That divergence is the thing worth seeing (§17.7).
  const moved = it.review && it.review.verdictAtReview !== it.verdict;

  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-3 border-b px-4 py-3 last:border-b-0">
      {/* The marks, side by side. Changed rings keep their colour and matched rings recede,
          so the delta reads before any text does (§12.6.2). */}
      {anyHash ? (
        <div className="flex shrink-0 items-center gap-2">
          <FingerprintMark insight={side("from")} size={44} emphasis={anyChanged ? changed : undefined} />
          <span className="text-xs text-muted-foreground" aria-hidden="true">
            →
          </span>
          <FingerprintMark insight={side("to")} size={44} emphasis={anyChanged ? changed : undefined} />
        </div>
      ) : (
        // Absence gets a slot of its own width, so rows stay aligned and "no facts reported"
        // is visible rather than inferred from a gap.
        <div className="flex h-11 w-[7.25rem] shrink-0 items-center justify-center border border-dashed text-[0.6875rem] uppercase tracking-wider text-muted-foreground">
          no hashes
        </div>
      )}

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <Link
            to={`/models/${it.model}/versions/${it.version}`}
            className="font-mono text-sm hover:underline underline-offset-4"
          >
            {it.model}@{it.version}
          </Link>
          <VerdictBadge v={it.verdict} />
          {it.declaredMethod && (
            <Tooltip content="Declared by whoever recorded the derivation — the intent, beside the measurement.">
              <span className="font-mono text-xs text-muted-foreground">
                declared {it.declaredMethod}
              </span>
            </Tooltip>
          )}
          {it.status === "closed" && (
            <Badge variant="muted">{it.review?.outcome ?? "reviewed"}</Badge>
          )}
        </div>

        <div className="mt-1 text-xs text-muted-foreground">
          derived from{" "}
          {it.derivedFrom ? (
            <Link
              to={`/models/${it.derivedFrom.model}/versions/${it.derivedFrom.version}`}
              className="font-mono hover:underline underline-offset-4"
            >
              {parent}
            </Link>
          ) : (
            <span className="font-mono">{parent}</span>
          )}{" "}
          · {relTime(it.edgeCreatedAt)}
          {it.missing?.length ? <> · missing {it.missing.join(", ")}</> : null}
        </div>

        {it.review && (
          <div className="mt-1 text-xs text-muted-foreground">
            {it.review.reviewedBy ? `${it.review.reviewedBy} · ` : ""}
            {relTime(it.review.reviewedAt)}
            {moved ? (
              <>
                {" "}
                · reviewed against <span className="font-mono">{it.review.verdictAtReview}</span>;
                the verdict has since moved to <span className="font-mono">{it.verdict}</span>
              </>
            ) : null}
            {it.review.note ? <> · “{it.review.note}”</> : null}
          </div>
        )}
      </div>

      <Button
        size="sm"
        variant={it.status === "open" ? "default" : "outline"}
        onClick={() => onReview(it)}
      >
        {it.status === "open" ? "Review" : "Re-review"}
      </Button>
    </div>
  );
}

/**
 * The queue, as a section of the compliance workspace. It fetches open and closed together:
 * "nothing to review" and "everything has been reviewed" are different answers, and a reader
 * deciding whether they are done needs to see which one they are looking at.
 */
export function ReviewQueue() {
  const { data, error, loading, reload } = useAsync(() => api.reviews(), []);
  const [editing, setEditing] = useState<ReviewItem | null>(null);

  if (loading || error || !data) return null;
  const items = data.items;
  const open = items.filter((i) => i.status === "open");
  const closed = items.filter((i) => i.status === "closed");

  // Nothing derived on any governed model. Rendering an empty card here would imply the
  // question is live when it is not.
  if (items.length === 0) return null;

  return (
    <section className="mt-6">
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-semibold">Derivations to review</h2>
        <span className="text-xs text-muted-foreground">
          Art. 25 · modifying a governed model can transfer provider duties. Flagged, never
          decided here.
        </span>
      </div>
      <div className="border bg-card">
        {open.length === 0 ? (
          <div className="px-4 py-8 text-center text-sm text-muted-foreground">
            Every derivation on a governed model has been reviewed.
          </div>
        ) : (
          open.map((it) => <ReviewRow key={it.edgeId} it={it} onReview={setEditing} />)
        )}
      </div>

      {closed.length > 0 && <ClosedList items={closed} onReview={setEditing} />}

      {editing && (
        <ReviewDialog
          item={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
    </section>
  );
}

function ClosedList({
  items,
  onReview,
}: {
  items: ReviewItem[];
  onReview: (it: ReviewItem) => void;
}) {
  const [show, setShow] = useState(false);
  return (
    <div className="mt-3">
      <button
        onClick={() => setShow((v) => !v)}
        className="flex w-full items-center justify-between border bg-card px-4 py-2.5 text-left text-sm hover:bg-accent"
      >
        <span className="font-semibold">Reviewed</span>
        <span className="text-xs text-muted-foreground">
          {show ? "Hide" : `${items.length} · show`}
        </span>
      </button>
      {show && (
        <div className="mt-3 border bg-card">
          {items.length === 0 ? (
            <Empty>Nothing reviewed yet.</Empty>
          ) : (
            items.map((it) => <ReviewRow key={it.edgeId} it={it} onReview={onReview} />)
          )}
        </div>
      )}
    </div>
  );
}
