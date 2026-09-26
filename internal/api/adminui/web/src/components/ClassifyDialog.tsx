import { useEffect, useRef, useState } from "react";
import { api, type Classification, type EUGpaiTier, type EUSystemRiskClass } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

// The classify form (§16.8/§16.9). It writes through the BFF to the same core operation the
// Model API exposes, so validation, the server-set anchor and the `classification.set` audit
// event are identical — the console is a client of the rules, not a way around them.
//
// Three things this form will not do, by design:
//
//  - It never preselects a class for an unclassified model. The default is `unclassified`,
//    because guessing "low" is the guess that costs you (§16.3), and a form that defaults to
//    `minimal` would be the registry deciding a legal question.
//  - It offers no way to clear a staleness other than making a real assessment. Submitting
//    *is* re-classifying; there is no "still fine" button (§16.9).
//  - It cannot set who classified it or when. Those come from the actor header and the
//    server clock, and the API rejects them if sent (§16.6).

const CLASSES: { value: EUSystemRiskClass; label: string; hint: string }[] = [
  { value: "unclassified", label: "Unclassified", hint: "Nobody has decided yet. A real answer." },
  { value: "minimal", label: "Minimal risk", hint: "No specific obligations." },
  { value: "limited", label: "Limited risk", hint: "Transparency obligations only." },
  { value: "high_annex_iii", label: "High risk · Annex III", hint: "Use case listed in Annex III." },
  { value: "high_annex_i", label: "High risk · Annex I", hint: "Safety component under Annex I." },
  { value: "prohibited", label: "Prohibited", hint: "Falls under a prohibited practice." },
];

const TIERS: { value: EUGpaiTier; label: string }[] = [
  { value: "none", label: "Not a general-purpose model" },
  { value: "gpai", label: "GPAI (Art. 53)" },
  { value: "gpai_systemic", label: "GPAI with systemic risk (Art. 55)" },
];

const isHighRisk = (c: EUSystemRiskClass) => c === "high_annex_iii" || c === "high_annex_i";

/** Date input ⇄ epoch millis. Empty means no review scheduled, which is a real choice. */
const toDateInput = (ms?: number) => (ms ? new Date(ms).toISOString().slice(0, 10) : "");
const fromDateInput = (s: string) => (s ? Date.parse(`${s}T00:00:00Z`) : null);

function Field({
  label,
  hint,
  required,
  children,
}: {
  label: string;
  hint?: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="label-caps">
        {label}
        {required && <span className="ml-1 text-destructive">required</span>}
      </span>
      {children}
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
}

export function ClassifyDialog({
  model,
  current,
  onClose,
  onSaved,
}: {
  model: string;
  current: Classification | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  // PUT replaces the row whole (§16.8), so the form opens pre-filled with what is stored and
  // the reader edits a complete assessment. Starting blank would quietly drop the basis that
  // is already on file.
  const [cls, setCls] = useState<EUSystemRiskClass>(current?.euSystemRiskClass ?? "unclassified");
  const [tier, setTier] = useState<EUGpaiTier>(current?.euGpaiTier ?? "none");
  const [purpose, setPurpose] = useState(current?.intendedPurpose ?? "");
  const [basis, setBasis] = useState(current?.basis ?? "");
  const [review, setReview] = useState(toDateInput(current?.reviewDueAt));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Mirrors §16.6 so the reader is told before the round-trip. The server checks the same
  // rules regardless — this is a courtesy, not the enforcement point.
  const needsPurpose = cls !== "unclassified" && purpose.trim() === "";
  const needsBasis = isHighRisk(cls) && basis.trim() === "";
  const reviewMs = fromDateInput(review);
  const reviewInPast = reviewMs !== null && reviewMs <= Date.now();
  const blocked = needsPurpose || needsBasis || reviewInPast;

  async function save() {
    setBusy(true);
    setError("");
    try {
      await api.setClassification(model, {
        euSystemRiskClass: cls,
        euGpaiTier: tier,
        intendedPurpose: purpose.trim(),
        basis: basis.trim(),
        reviewDueAt: reviewMs,
      });
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
      <div ref={ref} className="w-full max-w-xl border bg-card shadow-lg rounded-lg">
        <div className="border-b px-5 py-3">
          <div className="text-sm font-semibold">
            {current ? "Re-classify" : "Classify"} <span className="font-mono">{model}</span>
          </div>
          <div className="mt-0.5 text-xs text-muted-foreground">
            EU AI Act · recorded as a declared assessment, attributed to you and dated now
          </div>
        </div>

        <div className="space-y-4 px-5 py-4">
          <Field
            label="System risk class"
            hint={CLASSES.find((c) => c.value === cls)?.hint}
          >
            <select
              value={cls}
              onChange={(e) => setCls(e.target.value as EUSystemRiskClass)}
              className="border bg-transparent px-2 py-1.5 text-sm"
            >
              {CLASSES.map((c) => (
                <option key={c.value} value={c.value}>
                  {c.label}
                </option>
              ))}
            </select>
          </Field>

          <Field label="GPAI tier" hint="A separate question from risk class — the Act treats systems and general-purpose models differently.">
            <select
              value={tier}
              onChange={(e) => setTier(e.target.value as EUGpaiTier)}
              className="border bg-transparent px-2 py-1.5 text-sm"
            >
              {TIERS.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </select>
          </Field>

          <Field
            label="Intended purpose"
            required={cls !== "unclassified"}
            hint={needsPurpose ? undefined : "What this model is used for, in one sentence."}
          >
            <textarea
              value={purpose}
              onChange={(e) => setPurpose(e.target.value)}
              rows={2}
              placeholder="Scores card-not-present transactions for manual review."
              className="border bg-transparent px-2 py-1.5 text-sm"
            />
            {needsPurpose && (
              <span className="text-xs text-destructive">
                Nobody can review a class with no stated purpose.
              </span>
            )}
          </Field>

          <Field
            label="Basis"
            required={isHighRisk(cls)}
            hint={needsBasis ? undefined : "Why this class — the reasoning, not the conclusion."}
          >
            <textarea
              value={basis}
              onChange={(e) => setBasis(e.target.value)}
              rows={2}
              placeholder="Annex III §5(b) — creditworthiness adjacent; counsel review 2026-03-11."
              className="border bg-transparent px-2 py-1.5 text-sm"
            />
            {needsBasis && (
              <span className="text-xs text-destructive">
                A high-risk classification has to record why.
              </span>
            )}
          </Field>

          <Field label="Review due" hint="Leave empty for no scheduled review — that is a real choice, and it is shown as one.">
            <Input type="date" value={review} onChange={(e) => setReview(e.target.value)} className="max-w-[12rem]" />
            {reviewInPast && (
              <span className="text-xs text-destructive">The review date has to be in the future.</span>
            )}
          </Field>

          {current && (
            <p className="border-t pt-3 text-xs text-muted-foreground">
              This replaces the whole assessment on file — anything you clear here is cleared,
              so an old basis never sits underneath a new class. The previous values stay in
              the audit log.
            </p>
          )}

          {error && <div className="border border-destructive/50 bg-muted px-3 py-2 text-sm rounded-md">{error}</div>}
        </div>

        <div className="flex items-center justify-end gap-2 border-t px-5 py-3">
          <Button variant="outline" size="sm" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button size="sm" onClick={save} disabled={busy || blocked}>
            {busy ? "Saving…" : current ? "Record new assessment" : "Record classification"}
          </Button>
        </div>
      </div>
    </div>
  );
}
