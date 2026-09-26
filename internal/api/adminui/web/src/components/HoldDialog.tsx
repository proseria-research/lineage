import { useEffect, useState } from "react";
import { api, type Hold } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

// Place or lift a legal hold (§19.3, §19.8). It writes through the BFF to the same core
// operations the Model API exposes, so the `hold.set` / `hold.release` audit events and the
// required reason are identical — the console is a client of the rules, not a way around them.
//
// Three things this form will not do:
//
//  - It cannot set who placed the hold or when. Those come from the actor header and the
//    server clock, and the row carries nothing else (§19.7.1).
//  - It will not submit without a reason. The reason is recorded on the audit event and is
//    the only durable record of the matter — releasing with no stated reason is exactly the
//    record an auditor asks about and nobody can reconstruct (§19.3.1).
//  - It offers no "delete anyway". A hold refuses destruction and nothing overrides it.

export function HoldDialog({
  subject,
  release,
  current,
  onClose,
  onSaved,
}: {
  // model name, or `model@version` — the label the reader sees and the route it writes to.
  subject: { model: string; version?: string };
  release: boolean;
  current: Hold | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const label = subject.version ? `${subject.model} ${subject.version}` : subject.model;
  const blocked = reason.trim() === "";

  async function save() {
    setBusy(true);
    setError("");
    try {
      await api.setHold(subject, !release, reason.trim());
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
      <div className="w-full max-w-lg rounded-lg border bg-card shadow-xl">
        <div className="border-b px-5 py-4">
          <div className="text-base font-semibold">
            {release ? "Release legal hold on" : "Place legal hold on"}{" "}
            <span className="font-mono">{label}</span>
          </div>
          <div className="mt-0.5 text-sm text-muted-foreground">
            Recorded under your name and dated now, on the audit trail.
          </div>
        </div>

        <div className="space-y-4 px-5 py-4">
          <div className="rounded-md bg-brand-soft px-3.5 py-3 text-sm leading-relaxed text-brand">
            {release ? (
              <>
                <span className="font-medium">Releasing a hold </span>
                means it can be deleted again once your retention rules allow. Do this when the
                matter it was placed for is closed.
              </>
            ) : (
              <>
                <span className="font-medium">A legal hold </span>
                keeps records from being deleted while a legal matter is open — a regulator's
                inquiry, a lawsuit, an audit. Promoting, editing, publishing and archiving all keep
                working; only deletion is blocked.
              </>
            )}
          </div>
          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium">
              {release ? "Why is it being released?" : "What matter is this for?"}
              <span className="ml-1.5 text-xs font-normal text-muted-foreground">required</span>
            </span>
            <Input
              autoFocus
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={
                release ? "Matter closed — REF-2026-118" : "Regulator inquiry REF-2026-118"
              }
            />
            <span className="text-xs text-muted-foreground">
              Saved permanently on the audit trail, so an auditor can see why.
            </span>
          </label>

          {release && current ? (
            <p className="text-xs text-muted-foreground">
              Held since {new Date(current.heldSince).toISOString().slice(0, 10)}
              {current.heldBy ? ` by ${current.heldBy}` : ""}. The release is recorded as its own
              event, because it's exactly what an auditor will ask about.
            </p>
          ) : null}

          {!release ? (
            <p className="text-xs text-muted-foreground">
              Covers this{" "}
              {subject.version ? "version" : "model and every version under it"}. Nothing can
              override it until someone releases it.
            </p>
          ) : null}

          {error ? <p className="text-sm text-destructive">{error}</p> : null}
        </div>

        <div className="flex items-center justify-end gap-2 border-t px-5 py-3">
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy || blocked}>
            {busy ? "Saving…" : release ? "Release hold" : "Place hold"}
          </Button>
        </div>
      </div>
    </div>
  );
}
