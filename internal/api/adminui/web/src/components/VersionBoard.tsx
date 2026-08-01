import { Link } from "react-router-dom";
import type { Stage, VersionSummary } from "@/lib/api";
import { cn, relTime } from "@/lib/utils";

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
  production: "border-production bg-production/10 hover:border-production hover:bg-production/20",
  staging: "border-foreground",
  draft: "border-border",
  archived: "border-border border-dashed text-muted-foreground",
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
    <div className="grid grid-cols-1 gap-px border bg-border sm:grid-cols-2 lg:grid-cols-4">
      {ORDER.map((stage) => {
        const inStage = versions.filter((v) => v.stage === stage);
        return (
          <section
            key={stage}
            aria-label={`${stage} — ${inStage.length} version${inStage.length === 1 ? "" : "s"}`}
            className={cn("bg-card", stage === "production" && "border-t-2 border-production")}
          >
            <h3 className="flex items-center justify-between border-b px-3 py-2">
              <span className={cn("label-caps", stage === "production" && "font-semibold text-production")}>
                {stage}
              </span>
              <span className="label-caps tabular-nums">{inStage.length}</span>
            </h3>

            {inStage.length === 0 ? (
              <p className="label-caps px-3 py-4">empty</p>
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
                      "relative border px-3 py-2.5 transition-colors",
                      "hover:border-foreground hover:bg-accent",
                      "focus-within:border-foreground focus-within:bg-accent",
                      cardTone[stage],
                    )}
                  >
                    <Link
                      to={`/models/${model}/versions/${v.name}`}
                      className="font-mono text-sm font-medium after:absolute after:inset-0 after:content-['']"
                    >
                      {v.name}
                    </Link>
                    <div className="label-caps mt-1 truncate">
                      {v.author || "unattributed"} · {relTime(v.createdAt)}
                    </div>
                    {previous.has(v.id) && (
                      <Link
                        to={`/models/${model}/compare?from=${encodeURIComponent(previous.get(v.id)!)}&to=${encodeURIComponent(v.name)}`}
                        className="label-caps relative z-10 -mx-1 mt-1 inline-block px-1 py-0.5 hover:text-foreground hover:underline underline-offset-4"
                      >
                        vs {previous.get(v.id)}
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
