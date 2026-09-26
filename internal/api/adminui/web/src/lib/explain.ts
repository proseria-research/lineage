// Plain-language explanations for every governance term the console shows. The official
// vocabulary stays on the badges; this is what a person who is not a lawyer needs beside it.
// Kept factual and brief — what it means, why it matters, an example, what to do. Not legal
// advice, and the console says so wherever these appear.

export interface Explanation {
  /** The term as shown. */
  term: string;
  /** What it means, in everyday words. */
  plain: string;
  /** Why anyone should care. */
  why?: string;
  /** A concrete case. */
  example?: string;
  /** What to do about it, when there is something to do. */
  next?: string;
  /** Where it comes from, for people who want to check. */
  source?: string;
}

export const EXPLAIN: Record<string, Explanation> = {
  // ---- EU AI Act: risk classes ----
  prohibited: {
    term: "Prohibited",
    plain: "A use of AI the EU has banned outright.",
    why: "It cannot be placed on the EU market or used there at all.",
    example: "Social scoring of people, AI that manipulates behaviour to cause harm, untargeted scraping of faces to build recognition databases, emotion recognition at work or school.",
    next: "If this is right, the model should not be in service for EU use. Check with your legal team.",
    source: "EU AI Act, Article 5",
  },
  high_annex_iii: {
    term: "High risk (Annex III)",
    plain: "AI used for a purpose the EU lists as able to seriously affect people's lives or rights.",
    why: "It carries the heaviest duties: risk management, data quality, technical documentation, human oversight, logging, and registration before use.",
    example: "Credit scoring, screening job applicants, deciding access to benefits or education, exam grading, some law-enforcement and border uses.",
    next: "Keep the classification current and its basis written down; reviews and evidence matter most for these models.",
    source: "EU AI Act, Article 6(2) and Annex III",
  },
  high_annex_i: {
    term: "High risk (Annex I)",
    plain: "AI that is a safety part of a product already regulated in the EU, such as a medical device or a machine.",
    why: "It follows the high-risk rules as part of that product's own safety assessment.",
    example: "AI in a medical imaging device, a component of industrial machinery, a toy, or a vehicle system.",
    source: "EU AI Act, Article 6(1) and Annex I",
  },
  limited: {
    term: "Limited risk",
    plain: "AI whose main duty is to be open about being AI.",
    why: "People must be told they are dealing with AI, and generated content must be marked.",
    example: "Chatbots, systems that generate images, audio or video, deepfakes.",
    source: "EU AI Act, Article 50",
  },
  minimal: {
    term: "Minimal risk",
    plain: "Everyday AI with no specific duties under the Act.",
    example: "Spam filters, recommendations in a game, stock forecasting for a warehouse.",
    source: "EU AI Act",
  },
  unclassified: {
    term: "Not classified",
    plain: "Nobody has said yet which risk category this model falls into.",
    why: "Lineage never guesses. Until someone decides, the model's obligations are unknown — which is not the same as 'low risk'.",
    next: "Classify it from the Governance page. You record the category and why.",
  },

  // ---- EU AI Act: general-purpose AI ----
  gpai_none: {
    term: "Not a general-purpose model",
    plain: "The model is built for specific tasks rather than being a broad foundation model others build on.",
  },
  gpai: {
    term: "General-purpose AI model",
    plain: "A broad model that can do many kinds of tasks and is offered for others to build on, such as a large language model.",
    why: "Its provider must keep technical documentation, help downstream builders comply, have a copyright policy, and publish a summary of the training data.",
    source: "EU AI Act, Article 53",
  },
  gpai_systemic: {
    term: "General-purpose, with systemic risk",
    plain: "A general-purpose model so capable it could have broad effects — presumed when training used more than 10²⁵ floating-point operations.",
    why: "On top of the general duties: model evaluations including adversarial testing, serious-incident reporting, and cybersecurity protection.",
    source: "EU AI Act, Articles 51 and 55",
  },

  // ---- Staleness ----
  out_of_date: {
    term: "Out of date",
    plain: "Something changed after this model was assessed, so the assessment may no longer be accurate.",
    why: "An assessment describes the model as it was. A new version, a change in what's live, or a passed review date means nobody has looked at the model as it is now.",
    next: "Open the reason, check whether the category still fits, and record the assessment again — even if the answer is unchanged. Lineage never clears this on its own.",
  },
  current: {
    term: "Current",
    plain: "Assessed, and nothing relevant has changed since.",
  },

  // ---- Modification review (Art. 25) ----
  modification_review: {
    term: "Modification review",
    plain: "Someone changed a high-risk model — for example fine-tuned it — and a person needs to judge whether the change was big enough to matter legally.",
    why: "Under the EU AI Act, whoever makes a 'substantial modification' to a high-risk system can become its provider, taking on the provider's duties.",
    next: "Look at what was measured to have changed and what the team said they did, then record your judgement: substantial, not substantial, or undetermined.",
    source: "EU AI Act, Articles 3(23) and 25",
  },
  not_substantial: {
    term: "Not substantial",
    plain: "The reviewer judged the change too small to shift legal responsibility.",
  },
  substantial: {
    term: "Substantial",
    plain: "The reviewer judged the change significant enough that whoever made it may now carry the provider's duties.",
    next: "Bring in your compliance team: a new conformity assessment may be needed.",
  },
  undetermined_review: {
    term: "Undetermined",
    plain: "The reviewer could not decide yet, often because not enough about the change was reported.",
  },

  // ---- Measured changes (fingerprint verdicts) ----
  identical: {
    term: "Identical",
    plain: "The model files are byte-for-byte the same as the version it came from — a repackage.",
  },
  reweighted: {
    term: "Reweighted",
    plain: "Same architecture, new weight values — what retraining or fine-tuning produces.",
  },
  recast: {
    term: "Recast",
    plain: "Same architecture and shapes, stored at a different numeric precision — typically quantization (for example 32-bit to 8-bit).",
  },
  rescaled: {
    term: "Rescaled",
    plain: "The same design made wider or deeper — more or fewer layers or larger layers.",
  },
  rearchitected: {
    term: "Rearchitected",
    plain: "The architecture itself changed: new kinds of layers or a different backbone.",
  },
  unknown: {
    term: "Unknown change",
    plain: "Not enough fingerprint data was reported to say what kind of change this is.",
    next: "Ask the team that publishes this model to report the missing fingerprint hashes.",
  },

  // ---- Model risk management ----
  model_risk: {
    term: "Model risk",
    plain: "How much harm a model could cause if it were wrong, and whether it has been independently checked and is being watched in use.",
    why: "Banking supervisors expect every model to have a risk tier, independent validation, and ongoing monitoring once live.",
    source: "US SR 26-2, UK PRA SS1/23, Canada OSFI E-23",
  },
  tier_1: {
    term: "Tier 1",
    plain: "Highest materiality: errors could cause significant financial, customer or regulatory harm.",
    why: "Gets the most scrutiny — the deepest validation and the closest monitoring.",
    example: "An automated credit decision or a fraud model that blocks payments.",
  },
  tier_2: { term: "Tier 2", plain: "Material, with standard validation and monitoring." },
  tier_3: { term: "Tier 3", plain: "Low materiality: lighter validation is proportionate." },
  untiered: {
    term: "No tier",
    plain: "Nobody has assessed how material this model is.",
    next: "Set a tier from the model's Governance tab.",
  },
  out_of_scope: {
    term: "Out of scope",
    plain: "Your team judged this is not a model under the framework at all. The reason has to be written down.",
  },
  not_validated: {
    term: "Not validated",
    plain: "The version in use has no accepted independent validation.",
    next: "Have someone other than the model's author validate it and record the result on the version's Governance tab.",
  },
  unmonitored: {
    term: "Unmonitored",
    plain: "The model is live, but no evaluation has been recorded since it went live.",
    why: "Ongoing monitoring is the failure all three supervisory frameworks exist to catch.",
    next: "Record an evaluation of the production version.",
  },
  approved: { term: "Approved", plain: "The validator judged the version fit for its intended use." },
  conditional: {
    term: "Approved with conditions",
    plain: "Fit for use once specific conditions are met.",
    next: "When the conditions are met, mark them cleared on the version's Governance tab.",
  },
  rejected: { term: "Rejected", plain: "The validator judged the version not fit for its intended use." },
  not_independent: {
    term: "Not independent",
    plain: "The person who validated this version is also the one who built it, or wasn't named.",
    why: "Supervisors expect validation by someone independent of development. It's recorded, not blocked — small teams sometimes have no choice.",
  },

  // ---- Change control plans ----
  change_plan: {
    term: "Change control plan",
    plain: "A written, agreed list of the kinds of change a model may go through without a fresh review.",
    why: "Regulators such as the US FDA let AI medical devices change within a pre-agreed plan. Lineage checks every new version against the plan in force when it shipped.",
    source: "FDA predetermined change control plan (PCCP)",
  },
  within_plan: { term: "Within plan", plain: "The change this version made is one the plan allows." },
  outside_plan: {
    term: "Outside plan",
    plain: "This version made a kind of change the plan in force does not allow.",
    why: "It may need a new regulatory submission. Lineage reports this; it never blocks the release.",
    next: "Check with your regulatory team.",
  },
  cant_tell: {
    term: "Can't tell",
    plain: "Not enough fingerprint data was reported to know what kind of change this is, so it can't be checked against the plan.",
    next: "Report the missing fingerprint hashes when publishing.",
  },
  before_plan: { term: "Before any plan", plain: "This version shipped before any plan was in force. Not a violation." },
  no_plan: { term: "No plan", plain: "This model has no change control plan." },

  // ---- Records ----
  legal_hold: {
    term: "Legal hold",
    plain: "The model or version can't be deleted while a legal matter is open.",
    why: "Evidence must be preserved. Everything else — promoting, editing, archiving — still works.",
    next: "Release the hold when the matter closes.",
  },
  audit_integrity: {
    term: "Audit log integrity",
    plain: "Every change is written to a log that is sealed in batches with a chain of cryptographic hashes.",
    why: "If anyone edited or deleted a past entry, the check would fail and show where.",
  },
};

/** The explanation for a key, if there is one. */
export const explain = (key?: string | null): Explanation | undefined => (key ? EXPLAIN[key] : undefined);
