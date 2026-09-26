import { Link } from "react-router-dom";
import type { Stage, VersionSummary } from "@/lib/api";
import { cn, relTime } from "@/lib/utils";
import { STAGE_HELP, STAGE_LABEL } from "@/lib/labels";

// The version list as a stage board: one column per lifecycle stage (§02.4), so "what is in
// production" and "what is queued behind it" are answered by looking rather than by reading a
// stage column down a table.
//
// Emphasis is the fill / outline / dashed scale the stage badges use, transposed onto the
// cards: staging is outlined, draft is quiet, archived is dashed and dimmed. Production is
// the one stage carrying an invariant (at most one version per model, §02.4), so it is also
// the one stage tinted — with --production, the same green the website uses. The column
// already names the stage, so the cards carry no badge: position and heading are the label,
// and the tint only reinforces them.
//
// Each tone owns its hover state as well as its resting one. cn() runs through twMerge, so
// the tone is listed last and wins over the shared hover classes.

const ORDER: Stage[] = ["draft", "staging", "production", "archived"];

const cardTone: Record<Stage, string> = {
  production: "border-ok/40 bg-ok-soft hover:border-ok",
  staging: "border-brand/30 bg-brand-soft/60 hover:border-brand",
  draft: "border-border bg-card",
  archived: "border-dashed border-border bg-transparent text-muted-foreground",
};

export function VersionBoard({ model, versions }: { model: string; versions: VersionSummary[] }) {
  // Neighbours come from the full ordered list, not from within a column: "what changed in
  // this release" means against the previous version overall, whatever stage it ended up in.
  const previous = new Map<string, string>();
  versions.forEach((v, i) => {
    const older = versions[i + 1];
    if (older) previous.set(v.id, older.name);
  });

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {ORDER.map((stage) => {
        const inStage = versions.filter((v) => v.stage === stage);
        return (
          <section
            key={stage}
            aria-label={`${stage} — ${inStage.length} version${inStage.length === 1 ? "" : "s"}`}
            className="rounded-lg border bg-muted/60"
          >
            <h3 className="flex items-center justify-between px-3 pt-3 pb-1" title={STAGE_HELP[stage]}>
              <span className={cn("flex items-center gap-2 text-sm font-semibold", stage === "production" && "text-ok")}>
                {stage === "production" && <span className="h-2 w-2 rounded-full bg-ok" />}
                {STAGE_LABEL[stage]}
              </span>
              <span className="rounded-full bg-secondary px-1.5 text-xs tabular-nums text-muted-foreground">{inStage.length}</span>
            </h3>

            {inStage.length === 0 ? (
              <p className="px-3 pt-1 pb-4 text-xs text-muted-foreground">None</p>
            ) : (
              <ul className="flex min-h-24 flex-col gap-2 p-3">
                {inStage.map((v) => (
                  // The whole card opens the version. The name anchor is stretched over the
                  // card with an ::after overlay rather than wrapping everything in one link,
                  // which would nest the compare anchor inside it — invalid, and it would
                  // swallow the inner click. The compare link is lifted back above the
                  // overlay so it stays independently clickable.
                  <li
                    key={v.id}
                    className={cn(
                      "relative rounded-md border px-3 py-2.5 transition-colors",
                      "hover:border-foreground/40",
                      "focus-within:border-foreground/40",
                      cardTone[stage],
                    )}
                  >
                    <Link
                      to={`/models/${model}/versions/${v.name}`}
                      className="font-mono text-sm font-medium after:absolute after:inset-0 after:content-['']"
                    >
                      {v.name}
                    </Link>
                    <div className="mt-0.5 truncate text-xs text-muted-foreground">
                      {v.author || "unattributed"}, {relTime(v.createdAt)}
                    </div>
                    {previous.has(v.id) && (
                      <Link
                        to={`/models/${model}/compare?from=${encodeURIComponent(previous.get(v.id)!)}&to=${encodeURIComponent(v.name)}`}
                        className="relative z-10 -mx-1 mt-1 inline-block rounded px-1 py-0.5 text-xs font-medium text-brand hover:underline"
                      >
                        Compare with {previous.get(v.id)}
                      </Link>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
        );
      })}
    </div>
  );
}
