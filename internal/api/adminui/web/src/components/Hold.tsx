import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { HoldDialog } from "@/components/HoldDialog";
import { api, type Evidence, type Hold, type VerifyBreak } from "@/lib/api";
import { useAsync } from "@/lib/useAsync";
import { relTime } from "@/lib/utils";

// Legal hold and evidence integrity (§19.8).
//
// Two rules, both the same rule the classification components follow: never mark something
// without saying what it means, and never state a guarantee wider than the one that holds.
//
//  1. A hold marker always says *who* placed it and *when*, and — when it is inherited —
//     *which subject* is actually held. A version refused under a held model needs the
//     model's name, or the reader has nothing to release.
//  2. A verification result always reports the window it did not cover. `ok` alone would be
//     the most misleading thing this page could say, because rows in the open epoch are not
//     yet protected (§19.5.3).

export function HoldBadge({ inherited }: { inherited?: boolean }) {
  return (
    <Badge variant={inherited ? "outline" : "danger"}>
      {inherited ? "Held via model" : "Legal hold"}
    </Badge>
  );
}

// HoldAction is the button, and lives with the other actions in a page header — the same
// place StageActions sits. The *marker* is separate (HoldNote) and sits in the body, because
// a hold is state, not a control: it belongs where a reader looks for what is true about a
// subject, not where they look for what they can do to it.
//
// An inherited hold renders nothing. The button would release the *model*, which is not the
// subject on screen; HoldNote names the holder so the reader can go there instead.
export function HoldAction({
  subject,
  hold,
  inherited,
  onChanged,
}: {
  subject: { model: string; version?: string };
  hold: Hold | null;
  inherited?: boolean;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState(false);
  if (inherited) return null;

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        {hold ? "Release hold" : "Place legal hold"}
      </Button>
      {open ? (
        <HoldDialog
          subject={subject}
          release={!!hold}
          current={hold}
          onClose={() => setOpen(false)}
          onSaved={() => {
            setOpen(false);
            onChanged();
          }}
        />
      ) : null}
    </>
  );
}

// HoldNote is the marker itself: the badge, the provenance, and what the hold does and does
// not block. "A hold is not a freeze" is the part people get wrong.
export function HoldNote({ hold, heldSubject }: { hold: Hold; heldSubject?: string }) {
  return (
    <Card className="p-4 space-y-2">
      <div className="flex items-center gap-2">
        <HoldBadge inherited={!!heldSubject} />
        <span className="text-sm text-muted-foreground">
          since {relTime(hold.heldSince)}
          {hold.heldBy ? ` · ${hold.heldBy}` : ""}
        </span>
      </div>
      <p className="text-sm text-muted-foreground">
        {heldSubject ? (
          <>
            Held through <span className="font-mono">{heldSubject}</span>. Deleting this version
            is refused while that hold stands.
          </>
        ) : (
          <>Deletion is refused while this hold stands.</>
        )}{" "}
        Everything else still works — metadata edits, stage transitions, publishing and
        archival. A hold blocks destruction, not change.
      </p>
      <p className="text-xs text-muted-foreground">
        The reason it was placed is on the audit trail, not here: the row carries current
        state, the trail carries why.
      </p>
    </Card>
  );
}

const BREAK_TEXT: Record<VerifyBreak["kind"], string> = {
  root_mismatch: "a row inside this epoch was edited",
  leaf_count_mismatch: "a row was removed from this epoch, or added to it",
  prev_root_mismatch: "an epoch was removed from the chain",
};

// EvidencePanel is the install-level integrity strip. §19.8 keeps this off model pages
// because it is a property of the install; this is the install-level page.
export function EvidencePanel() {
  const { data: loaded, error: loadError } = useAsync(() => api.evidence(), []);
  // The recompute replaces what the cheap load returned, so the panel holds it locally.
  const [verified, setVerified] = useState<Evidence | null>(null);
  const [verifying, setVerifying] = useState(false);
  const [error, setError] = useState<string>("");

  async function verify() {
    setVerifying(true);
    setError("");
    try {
      setVerified(await api.verifyEvidence());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setVerifying(false);
    }
  }

  const data = verified ?? loaded;
  if (!data) return null;

  const { retention, attestation, verify: result } = data;
  const floor = retention.minArchivedVersionDays;

  return (
    <Card className="p-4 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="font-medium">Audit log integrity</h2>
          <p className="text-sm text-muted-foreground">
            Settings for this whole install, not any one model.
          </p>
        </div>
        {attestation.enabled ? (
          <Button variant="outline" size="sm" onClick={verify} disabled={verifying}>
            {verifying ? "Verifying…" : "Verify audit log"}
          </Button>
        ) : null}
      </div>

      <dl className="grid gap-3 sm:grid-cols-2 text-sm">
        <div>
          <dt className="label-caps text-muted-foreground">Records are kept for at least</dt>
          {/* 0 is a real choice, not an unset value — so it says so in words (§19.4). */}
          <dd>{floor > 0 ? `${floor} days` : "no minimum set"}</dd>
        </div>
        <div>
          <dt className="label-caps text-muted-foreground">Tamper-evident sealing</dt>
          <dd>
            {attestation.enabled
              ? `On, sealed every ${attestation.sealIntervalSeconds} seconds`
              : "disabled"}
          </dd>
        </div>
      </dl>

      {result ? (
        <div className="space-y-2 border-t pt-3">
          <div className="flex items-center gap-2">
            {/* `ok` over zero epochs is true and means nothing — no break was found because
                nothing was checked. Badging that "verified" would be the widest claim on the
                page resting on the least evidence, so an empty scan says so instead. It is
                the normal state for the first sealing interval after a fresh start. */}
            <Badge variant={!result.ok ? "danger" : result.epochsChecked === 0 ? "dashed" : "ok"}>
              {!result.ok ? "Tampering detected" : result.epochsChecked === 0 ? "Nothing sealed yet" : "Intact"}
            </Badge>
            <span className="text-sm text-muted-foreground">
              {result.epochsChecked === 0
                ? `No batch has been sealed yet — the first seals after ${attestation.sealIntervalSeconds} seconds.`
                : `Re-checked ${result.leavesChecked} event${result.leavesChecked === 1 ? "" : "s"} in ${result.epochsChecked} sealed batch${result.epochsChecked === 1 ? "" : "es"}; nothing was altered.`}
            </span>
          </div>

          {result.firstBreak ? (
            <p className="text-sm">
              Batch {result.firstBreak.epoch}: {BREAK_TEXT[result.firstBreak.kind]}.{" "}
              {result.firstBreak.sealedAt ? `Sealed ${relTime(result.firstBreak.sealedAt)}.` : ""}
            </p>
          ) : null}

          {/* Never report a clean verify without its limits. Rows in the open window carry
              no root yet, and rows written before attestation was enabled never will. */}
          <p className="text-xs text-muted-foreground">
            Only sealed batches can be checked. Changes made in the last{" "}
            {relTime(result.openEpochSince).replace(" ago", "")} are not sealed yet.
            {result.attestationStartedAt > 0
              ? ` Sealing covers everything recorded since ${relTime(result.attestationStartedAt)}.`
              : " Nothing has been sealed on this install yet."}
          </p>
        </div>
      ) : null}

      {error || loadError ? (
        <p className="text-sm text-muted-foreground">{error || loadError}</p>
      ) : null}
    </Card>
  );
}
