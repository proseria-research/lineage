import { useEffect, useState } from "react";
import { api, type ReviewItem, type ReviewOutcome } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { VerdictBadge, VERDICT_TEXT } from "@/components/Review";
import { ClassBadge } from "@/components/Classification";

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
    hint: "Choose this when the change stays within what was originally assessed — for example a routine retrain on the same kind of data, or a quantization that keeps accuracy. Responsibility stays with the original provider.",
  },
  {
    value: "substantial",
    label: "Substantial",
    hint: "Choose this when the change affects how the model meets its requirements, or what it is used for — for example retraining it for a new purpose, or a change that materially alters its accuracy. Whoever made the change may now carry the provider's duties (Art. 25), and a new conformity assessment may be needed (Art. 43(4)).",
  },
  {
    value: "undetermined",
    label: "Undetermined",
    hint: "Choose this when you can't decide yet — for example because the measurement is incomplete. It records that you looked, which is different from nobody having opened it.",
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
    ? `${item.derivedFrom.model} ${item.derivedFrom.version}`
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
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-foreground/20 p-4 backdrop-blur-sm sm:p-10"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="w-full max-w-xl rounded-lg border bg-card shadow-xl">
        <div className="border-b px-5 py-4">
          <div className="text-base font-semibold">
            Review the change to <span className="font-mono">{item.model} {item.version}</span>
          </div>
          <div className="mt-0.5 text-sm text-muted-foreground">
            Recorded under your name and dated now, on the audit trail.
          </div>
        </div>

        <div className="space-y-4 px-5 py-4">
          {/* What is being judged, restated: the measurement, the declared intent, and where
              it came from. A reviewer should not have to remember the row they clicked. */}
          <div className="rounded-md bg-brand-soft px-3.5 py-3 text-sm leading-relaxed text-brand">
            <span className="font-medium">What you're deciding: </span>
            whether this change is big enough that whoever made it takes on legal responsibility
            for the model under the EU AI Act. Lineage measured what changed; you judge what it
            means. Not legal advice — involve your compliance team if unsure.
          </div>

          <dl className="grid gap-3 rounded-md border px-3.5 py-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground">Made from</dt>
              <dd className="flex flex-wrap items-center gap-2">
                <span className="font-mono">{parent}</span>
                {item.euSystemRiskClass && item.euSystemRiskClass !== "unclassified" && (
                  <ClassBadge cls={item.euSystemRiskClass} />
                )}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">What changed, as measured</dt>
              <dd className="flex flex-wrap items-center gap-2">
                <VerdictBadge v={item.verdict} />
                <span className="text-muted-foreground">{VERDICT_TEXT[item.verdict].replace(/^Measured: /, "")}</span>
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">What the team said they did</dt>
              <dd>{item.declaredMethod ?? <span className="text-muted-foreground">Not stated</span>}</dd>
            </div>
            {item.verdict === "unknown" && (
              <div className="text-xs text-muted-foreground">
                {item.missing?.length ? <>No {item.missing.join(", ")} hash was reported. </> : null}
                {item.candidates?.length ? <>It is one of: {item.candidates.join(" or ")}. </> : null}
                This does not mean nothing changed — only that there isn't enough data to say what did.
              </div>
            )}
          </dl>

          <fieldset className="flex flex-col gap-1.5">
            <legend className="mb-1.5 text-sm font-medium">Your judgement</legend>
            {OUTCOMES.map((o) => (
              <label
                key={o.value}
                className={[
                  "flex cursor-pointer gap-2.5 rounded-md border px-3 py-2.5",
                  outcome === o.value ? "border-brand bg-brand-soft" : "hover:bg-muted",
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
                  <span className="text-sm font-medium">{o.label}</span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">{o.hint}</span>
                </span>
              </label>
            ))}
          </fieldset>

          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium">Your reasoning</span>
            <textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              rows={3}
              placeholder="Quantization only; intended purpose and performance envelope unchanged."
              className="rounded-md border border-input bg-card px-3 py-2 text-sm"
            />
            <span className="text-xs text-muted-foreground">
              Optional, but it's the only place your reasoning is kept. The measured change is
              saved beside it and can't be edited later.
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
