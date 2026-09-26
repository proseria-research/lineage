import { useState } from "react";
import { ChevronDown, Lightbulb } from "lucide-react";
import { EXPLAIN } from "@/lib/explain";
import { cn } from "@/lib/utils";

// The "how this works" panel at the top of a governance tab: what the programme is, in plain
// words; three steps covering what you do, what Lineage watches and when it asks you to look;
// and what every label on the tab means. Collapsible, and the choice is remembered per tab.

export interface Guide {
  summary: string;
  steps: [string, string, string];
  /** EXPLAIN keys for the labels this tab uses. */
  labels: string[];
  source?: string;
}

const storeKey = (id: string) => `lineage.howItWorks.${id}`;

export function HowItWorks({ id, guide }: { id: string; guide: Guide }) {
  const [open, setOpen] = useState(() => {
    try {
      return localStorage.getItem(storeKey(id)) !== "closed";
    } catch {
      return true;
    }
  });
  const toggle = () => {
    setOpen((o) => {
      try {
        localStorage.setItem(storeKey(id), o ? "closed" : "open");
      } catch {
        /* per-viewer convenience only */
      }
      return !o;
    });
  };

  return (
    <section className="mb-6 rounded-lg border bg-card">
      <button
        onClick={toggle}
        aria-expanded={open}
        className="flex w-full items-center gap-2.5 px-5 py-3 text-left text-sm font-medium"
      >
        <Lightbulb className="h-4 w-4 text-brand" strokeWidth={2} />
        How this works
        <ChevronDown className={cn("ml-auto h-4 w-4 text-muted-foreground transition-transform", open && "rotate-180")} />
      </button>
      {open && (
        <div className="border-t px-5 pb-5 pt-4">
          <p className="max-w-3xl text-[0.9375rem] leading-relaxed">{guide.summary}</p>

          <ol className="mt-4 grid gap-3 md:grid-cols-3">
            {guide.steps.map((s, i) => (
              <li key={i} className="flex gap-3 rounded-md bg-muted px-3.5 py-3 text-sm leading-relaxed">
                <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-brand text-xs font-semibold text-primary-foreground">
                  {i + 1}
                </span>
                <span>{s}</span>
              </li>
            ))}
          </ol>

          {guide.labels.length > 0 && (
            <>
              <h3 className="mb-2 mt-5 text-sm font-semibold">What the labels mean</h3>
              <dl className="grid gap-x-6 gap-y-2.5 text-sm md:grid-cols-2">
                {guide.labels.map((k) => {
                  const e = EXPLAIN[k];
                  if (!e) return null;
                  return (
                    <div key={k}>
                      <dt className="font-medium">{e.term}</dt>
                      <dd className="text-muted-foreground">
                        {e.plain}
                        {e.example ? <span className="block text-xs">For example: {e.example}</span> : null}
                      </dd>
                    </div>
                  );
                })}
              </dl>
            </>
          )}

          <p className="mt-4 text-xs text-muted-foreground">
            {guide.source ? `${guide.source}. ` : ""}Hover or click any label on this page for more. This is guidance on
            how Lineage works, not legal advice.
          </p>
        </div>
      )}
    </section>
  );
}

export const GUIDES: Record<string, Guide> = {
  todo: {
    summary:
      "One list of everything across the governance programmes that needs a person. Each item says what happened and links to where you deal with it. Lineage records and flags; it never blocks a release and never makes a legal decision for you.",
    steps: [
      "Your team publishes and promotes models as usual.",
      "Lineage compares every change against the assessments, reviews and plans you have recorded.",
      "When something no longer lines up, it appears here with the reason and a link to fix it.",
    ],
    labels: [],
  },
  eu: {
    summary:
      "The EU AI Act sorts AI by how much harm its use could do. Higher-risk uses carry more duties. You decide which category each model is in and write down why; Lineage keeps that record and tells you when a change means it should be looked at again.",
    steps: [
      "Classify each model: pick its risk category and write down its intended purpose and your reasoning.",
      "Lineage watches for a new version, a change in what's in production, or a passed review date.",
      "When one happens, the model shows as out of date until someone reassesses it.",
    ],
    labels: ["prohibited", "high_annex_iii", "high_annex_i", "limited", "minimal", "unclassified", "gpai", "gpai_systemic", "out_of_date"],
    source: "Regulation (EU) 2024/1689",
  },
  reviews: {
    summary:
      "If someone takes a high-risk model and changes it — fine-tunes it, quantizes it — the EU AI Act may treat them as its provider, with all the duties that brings. Lineage measures what actually changed from the model fingerprints and puts each such change in front of a person to judge.",
    steps: [
      "A new version records that it was derived from a high-risk model.",
      "Lineage measures what kind of change it was — weights, precision, size or architecture.",
      "A reviewer reads the measurement beside what the team said they did, and records a judgement.",
    ],
    labels: ["modification_review", "reweighted", "recast", "rescaled", "rearchitected", "unknown", "not_substantial", "substantial"],
    source: "EU AI Act, Articles 3(23) and 25",
  },
  risk: {
    summary:
      "Banking supervisors expect firms to know how much each model could hurt them, to have it checked by someone independent, and to keep watching it once it is live. You set each model's tier and record validations; Lineage tells you when a validation is missing, expired, or when a live model has stopped being monitored.",
    steps: [
      "Set each model's tier — how much harm an error could cause.",
      "An independent validator checks each version and records their conclusion.",
      "Lineage flags missing or expired validations, open conditions, and live models with no recent evaluation.",
    ],
    labels: ["tier_1", "tier_2", "tier_3", "out_of_scope", "not_validated", "conditional", "unmonitored", "not_independent"],
    source: "SR 26-2, PRA SS1/23, OSFI E-23",
  },
  plans: {
    summary:
      "A change control plan lists, in advance, the kinds of change a model may make without a fresh review — an approach the US FDA uses for AI medical devices. Lineage checks every new version against the plan that was in force when it shipped, and reports anything outside it. It never blocks the release.",
    steps: [
      "Declare a plan on the model's Governance tab: which kinds of change it allows, and by which methods.",
      "Each new derived version is measured and compared with the plan in force when it shipped.",
      "Versions outside the plan, or that can't be measured, appear here for your regulatory team.",
    ],
    labels: ["within_plan", "outside_plan", "cant_tell", "before_plan"],
    source: "FDA predetermined change control plans",
  },
  integrity: {
    summary:
      "Every change anyone makes is written to the audit log in the same step as the change itself. The log is sealed in batches with a chain of hashes, so if anyone later edited or deleted an entry, the check below would fail and show where.",
    steps: [
      "Each change is recorded the moment it happens.",
      "Every few seconds the newest entries are sealed into the hash chain.",
      "Run the check at any time to confirm nothing sealed has been altered.",
    ],
    labels: ["audit_integrity", "legal_hold"],
  },
};
