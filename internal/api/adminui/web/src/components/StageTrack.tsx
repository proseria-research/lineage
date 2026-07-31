import type { AuditEvent, Stage, VersionSummary } from "@/lib/api";
import { cn, fmtTime, relTime } from "@/lib/utils";

// A compact stage track for one version: the lifecycle line (§02.4) with the stages this
// version actually reached marked and dated. Entry times are read off the version's own
// audit events, so the track is a record of what happened, not a decoration — a version
// demoted out of production still shows when it was there.
//
// Monochrome like the rest of the console: squares and 1px rails, no color. A filled square
// is where the version is now, a small dot is a stage it passed through, a dashed outline is
// one it never reached.

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

  return (
    <div>
      <div className="flex items-start" role="img" aria-label={`Stage track — currently ${version.stage}`}>
        {ORDER.map((s, i) => {
          const here = s === version.stage;
          const seen = visited(s);
          const next = ORDER[i + 1];
          return (
            <div key={s} className="flex flex-1 flex-col items-center">
              <div className="flex w-full items-center">
                <Rail show={i > 0} solid={seen} />
                <span
                  title={seen ? `${s} · ${fmtTime(entered.get(s))}${reason.get(s) ? ` · ${reason.get(s)}` : ""}` : `${s} · not reached`}
                  className={cn(
                    "flex h-4 w-4 shrink-0 items-center justify-center border",
                    seen ? "border-foreground" : "border-dashed border-border",
                  )}
                >
                  {here ? (
                    <span className="h-2 w-2 bg-foreground" />
                  ) : seen ? (
                    <span className="h-1 w-1 bg-foreground" />
                  ) : null}
                </span>
                <Rail show={i < ORDER.length - 1} solid={next ? visited(next) : false} />
              </div>
              <div className={cn("label-caps mt-2", here && "font-semibold text-foreground")}>{s}</div>
              <div className="font-mono text-[0.6875rem] tabular-nums text-muted-foreground">
                {seen ? relTime(entered.get(s)) : "—"}
              </div>
            </div>
          );
        })}
      </div>
      <div className="label-caps mt-3 border-t pt-2">
        {moves === 0
          ? `No stage changes · still in ${version.stage}`
          : `${moves} stage change${moves === 1 ? "" : "s"} · now in ${version.stage}`}
      </div>
    </div>
  );
}

// Rail is half a connector segment; it stays in the layout when hidden so every marker keeps
// its column centre.
function Rail({ show, solid }: { show: boolean; solid: boolean }) {
  if (!show) return <div className="flex-1" />;
  return <div className={cn("flex-1", solid ? "h-px bg-foreground" : "border-t border-dashed border-border")} />;
}
