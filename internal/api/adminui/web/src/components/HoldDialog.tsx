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

  const label = subject.version ? `${subject.model}@${subject.version}` : subject.model;
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
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-background/80 p-6 backdrop-blur-sm"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="w-full max-w-lg border bg-card shadow-lg rounded-lg">
        <div className="border-b px-5 py-3">
          <div className="text-sm font-semibold">
            {release ? "Release legal hold on" : "Place legal hold on"}{" "}
            <span className="font-mono">{label}</span>
          </div>
          <div className="mt-0.5 text-xs text-muted-foreground">
            Recorded against you and dated now, on the audit trail
          </div>
        </div>

        <div className="space-y-4 px-5 py-4">
          <label className="flex flex-col gap-1.5">
            <span className="label-caps">
              Reason<span className="ml-1 text-destructive">required</span>
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
              Goes on the audit event, not on the record. The next hold would overwrite it
              there; on the trail it is permanent.
            </span>
          </label>

          {release && current ? (
            <p className="text-xs text-muted-foreground">
              Held since {new Date(current.heldSince).toISOString().slice(0, 10)}
              {current.heldBy ? ` by ${current.heldBy}` : ""}. Releasing is what an auditor
              looks for, so it is recorded as its own event.
            </p>
          ) : null}

          {!release ? (
            <p className="text-xs text-muted-foreground">
              Blocks deletion of this{" "}
              {subject.version ? "version" : "model and every version under it"}, and nothing
              overrides it. Metadata edits, stage transitions, publishing and archival all
              keep working — a hold is not a freeze.
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
