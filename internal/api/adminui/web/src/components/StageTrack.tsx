import { Archive, Check, FlaskConical, PencilLine, Rocket } from "lucide-react";
import type { AuditEvent, Stage, VersionSummary } from "@/lib/api";
import { cn, fmtTime, relTime } from "@/lib/utils";
import { STAGE_HELP, STAGE_LABEL } from "@/lib/labels";

// A compact stage track for one version: the lifecycle line (§02.4) with the stages this
// version actually reached marked and dated. Entry times are read off the version's own
// audit events, so the track is a record of what happened, not a decoration — a version
// demoted out of production still shows when it was there.
//
// A stepper: each stage is a disc with its icon. Where the version is now is filled in the
// stage's colour and ringed; a stage it passed through is a tick; one it never reached is a
// dashed outline. The rail between two stages is solid once the version reached the second.

const ORDER: Stage[] = ["draft", "staging", "production", "archived"];

export function StageTrack({ version, audit }: { version: VersionSummary; audit: AuditEvent[] }) {
  const entered = new Map<Stage, number>([["draft", version.createdAt]]);
  const reason = new Map<Stage, string>();
  let moves = 0;

  for (const e of audit) {
    if (e.action !== "version.stage_changed") continue;
    moves++;
    const to = e.data?.to as Stage | undefined;
    if (!to || !ORDER.includes(to)) continue;
    // Keep the most recent entry into each stage (a version can re-enter one).
    if (e.at >= (entered.get(to) ?? 0)) {
      entered.set(to, e.at);
      if (e.data?.reason) reason.set(to, e.data.reason);
    }
  }
  // The audit page is capped, so fall back to updatedAt rather than showing "never reached"
  // for the stage the version is demonstrably in.
  if (!entered.has(version.stage)) entered.set(version.stage, version.updatedAt);

  const visited = (s: Stage) => entered.has(s);

  const ICON: Record<Stage, typeof Rocket> = { draft: PencilLine, staging: FlaskConical, production: Rocket, archived: Archive };
  const TONE: Record<Stage, { solid: string; soft: string; text: string }> = {
    draft: { solid: "bg-foreground/70", soft: "bg-secondary", text: "text-foreground" },
    staging: { solid: "bg-brand", soft: "bg-brand-soft", text: "text-brand" },
    production: { solid: "bg-ok", soft: "bg-ok-soft", text: "text-ok" },
    archived: { solid: "bg-muted-foreground", soft: "bg-secondary", text: "text-muted-foreground" },
  };

  return (
    <div>
      <ol className="grid grid-cols-4" aria-label={`Lifecycle — currently ${STAGE_LABEL[version.stage]}`}>
        {ORDER.map((s, i) => {
          const here = s === version.stage;
          const seen = visited(s);
          const Icon = here ? ICON[s] : seen ? Check : ICON[s];
          const tone = TONE[s];
          const leftSolid = i > 0 && seen;
          const rightSolid = i < ORDER.length - 1 && visited(ORDER[i + 1]);
          return (
            <li key={s} className="flex flex-col items-center text-center" title={STAGE_HELP[s]}>
              <div className="flex w-full items-center">
                <Rail show={i > 0} solid={leftSolid} />
                <span
                  className={cn(
                    "flex h-9 w-9 shrink-0 items-center justify-center rounded-full",
                    here && [tone.solid, "text-white ring-4", s === "production" ? "ring-ok-soft" : s === "staging" ? "ring-brand-soft" : "ring-secondary"],
                    !here && seen && [tone.soft, tone.text],
                    !seen && "border-2 border-dashed border-border text-muted-foreground/60",
                  )}
                >
                  <Icon className="h-4 w-4" strokeWidth={2} />
                </span>
                <Rail show={i < ORDER.length - 1} solid={rightSolid} />
              </div>
              <div className={cn("mt-2.5 text-sm", here ? "font-semibold" : seen ? "font-medium" : "text-muted-foreground")}>
                {STAGE_LABEL[s]}
              </div>
              <div className="text-xs text-muted-foreground" title={seen ? fmtTime(entered.get(s)) : undefined}>
                {seen ? relTime(entered.get(s)) : "Not reached"}
              </div>
              {seen && reason.get(s) && (
                <div className="mt-1 max-w-[12rem] text-xs italic text-muted-foreground">“{reason.get(s)}”</div>
              )}
            </li>
          );
        })}
      </ol>
      <p className="mt-4 border-t pt-3 text-sm text-muted-foreground">
        {moves === 0
          ? `Still in ${STAGE_LABEL[version.stage].toLowerCase()}; it hasn't moved since it was published.`
          : `Moved ${moves} time${moves === 1 ? "" : "s"}. Now in ${STAGE_LABEL[version.stage].toLowerCase()}, since ${fmtTime(entered.get(version.stage))}.`}
      </p>
    </div>
  );
}

// Rail is half a connector segment; it stays in the layout when hidden so every disc keeps its
// column centre.
function Rail({ show, solid }: { show: boolean; solid: boolean }) {
  if (!show) return <div className="flex-1" />;
  return <div className={cn("h-0.5 flex-1 rounded-full", solid ? "bg-foreground/35" : "border-t-2 border-dashed border-border bg-transparent")} />;
}
