import { useEffect, useState } from "react";
import { api, type ReviewItem, type ReviewOutcome } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { VerdictBadge } from "@/components/Review";

// Record what a human concluded about one derivation (§17.5.1, §17.6.1). It writes through
// the BFF to the same core operation the Model API exposes, so the frozen verdict and the
// `review.record` audit event are identical — the console is a client of the rules, not a way
// around them.
//
// Three things this form will not do:
//
//  - It cannot set the verdict. The server reads it from the hashes at the moment of writing
//    and freezes it; the whole value of the field is that the registry witnessed the verdict
//    rather than accepting a claim about it, and the API rejects a supplied one (§17.6.1).
//  - It cannot edit or withdraw an earlier review. Submitting again appends a new row and the
//    old one stands — what somebody concluded before is part of the record (§17.4).
//  - It does not decide anything. The three outcomes are the reader's judgement, recorded;
//    nothing in the registry acts on which one is chosen (§17.1).

const OUTCOMES: { value: ReviewOutcome; label: string; hint: string }[] = [
  {
    value: "not_substantial",
    label: "Not substantial",
    hint: "The change does not affect compliance or intended purpose. Provider duties stay where they were.",
  },
  {
    value: "substantial",
    label: "Substantial",
    hint: "Art. 25 is in play — whoever made this change may now carry the provider's obligations.",
  },
  {
    value: "undetermined",
    label: "Undetermined",
    hint: "Looked at it, cannot resolve yet. A real answer, and distinguishable from nobody having opened it.",
  },
];

export function ReviewDialog({
  item,
  onClose,
  onSaved,
}: {
  item: ReviewItem;
  onClose: () => void;
  onSaved: () => void;
}) {
  // No preselection. An outcome is a legal judgement, and a form that opens on
  // "not substantial" is the registry nudging the cheap answer.
  const [outcome, setOutcome] = useState<ReviewOutcome | "">("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const parent = item.derivedFrom
    ? `${item.derivedFrom.model}@${item.derivedFrom.version}`
    : item.derivedFromRef;

  async function save() {
    if (!outcome) return;
    setBusy(true);
    setError("");
    try {
      await api.recordReview(item.model, item.version, { edgeId: item.edgeId, outcome, note: note.trim() });
      onSaved();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-background/80 p-6 backdrop-blur-sm"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="w-full max-w-xl border bg-card shadow-lg rounded-lg">
        <div className="border-b px-5 py-3">
          <div className="text-sm font-semibold">
            Review <span className="font-mono">{item.model}@{item.version}</span>
          </div>
          <div className="mt-0.5 text-xs text-muted-foreground">
            Art. 25 · recorded against you and dated now, on the audit trail
          </div>
        </div>

        <div className="space-y-4 px-5 py-4">
          {/* What is being judged, restated: the measurement, the declared intent, and where
              it came from. A reviewer should not have to remember the row they clicked. */}
          <div className="space-y-1.5 border px-3 py-2.5 text-xs rounded-md">
            <div className="flex flex-wrap items-center gap-2">
              <VerdictBadge v={item.verdict} />
              {item.declaredMethod && (
                <span className="font-mono text-muted-foreground">
                  declared {item.declaredMethod}
                </span>
              )}
              {item.euSystemRiskClass && item.euSystemRiskClass !== "unclassified" && (
                <Badge variant="muted">{item.euSystemRiskClass}</Badge>
              )}
              {item.euGpaiTier && item.euGpaiTier !== "none" && (
                <Badge variant="muted">{item.euGpaiTier}</Badge>
              )}
            </div>
            <div className="text-muted-foreground">
              derived from <span className="font-mono">{parent}</span>
            </div>
            {item.verdict === "unknown" && (
              <div className="text-muted-foreground">
                {item.missing?.length ? (
                  <>
                    Missing <span className="font-mono">{item.missing.join(", ")}</span>.{" "}
                  </>
                ) : null}
                {item.candidates?.length ? <>Narrowed to {item.candidates.join(" or ")}. </> : null}
                Nothing here says the model is unchanged — only that the facts do not reach.
              </div>
            )}
          </div>

          <fieldset className="flex flex-col gap-1.5">
            <legend className="label-caps mb-1.5">
              Outcome<span className="ml-1 text-destructive">required</span>
            </legend>
            {OUTCOMES.map((o) => (
              <label
                key={o.value}
                className={[
                  "flex cursor-pointer gap-2.5 border px-3 py-2",
                  outcome === o.value ? "border-foreground" : "hover:bg-accent",
                ].join(" ")}
              >
                <input
                  type="radio"
                  name="outcome"
                  value={o.value}
                  checked={outcome === o.value}
                  onChange={() => setOutcome(o.value)}
                  className="mt-0.5 shrink-0 accent-current"
                />
                <span>
                  <span className="text-sm">{o.label}</span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">{o.hint}</span>
                </span>
              </label>
            ))}
          </fieldset>

          <label className="flex flex-col gap-1.5">
            <span className="label-caps">Note</span>
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              rows={3}
              placeholder="Quantization only; intended purpose and performance envelope unchanged."
              className="border bg-transparent px-2 py-1.5 text-sm"
            />
            <span className="text-xs text-muted-foreground">
              Optional, and the only place your reasoning is recorded. The verdict the registry
              witnessed is stored alongside it and cannot be edited later.
            </span>
          </label>

          {error ? <p className="text-sm text-destructive">{error}</p> : null}
        </div>

        <div className="flex items-center justify-end gap-2 border-t px-5 py-3">
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy || !outcome}>
            {busy ? "Saving…" : "Record review"}
          </Button>
        </div>
      </div>
    </div>
  );
}
