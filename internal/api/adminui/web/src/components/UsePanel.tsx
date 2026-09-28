import { useState } from "react";
import { Check, Copy } from "lucide-react";
import type { Artifact, Stage } from "@/lib/api";
import { useClientConfig } from "@/lib/useClientConfig";
import { STAGE_LABEL } from "@/lib/labels";
import { cn } from "@/lib/utils";

// How to use a model from your own code — the part of a registry people come for. Snippets are
// filled in with this registry's Model API address and this model, for the four ways people
// consume it: plain HTTP, the Python SDK, the CLI, and KServe's lineage:// storage URI.

type Lang = "curl" | "python" | "cli" | "kserve";

const LANGS: { id: Lang; label: string }[] = [
  { id: "curl", label: "HTTP (curl)" },
  { id: "python", label: "Python" },
  { id: "cli", label: "CLI" },
  { id: "kserve", label: "KServe" },
];

export function CopyBlock({ code, label }: { code: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="relative">
      {label && <div className="mb-1 text-xs text-muted-foreground">{label}</div>}
      <pre className="overflow-x-auto rounded-md border bg-muted px-4 py-3 pr-12 font-mono text-[0.8125rem] leading-relaxed">
        {code}
      </pre>
      <button
        onClick={() => {
          navigator.clipboard?.writeText(code).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
        className="absolute right-2 top-2 rounded-md border bg-card p-1.5 text-muted-foreground hover:text-foreground"
        style={label ? { top: "1.75rem" } : undefined}
        aria-label="Copy"
        title="Copy"
      >
        {copied ? <Check className="h-4 w-4 text-ok" /> : <Copy className="h-4 w-4" />}
      </button>
    </div>
  );
}

export function UsePanel({
  model,
  version,
  stages,
  artifacts,
  format,
}: {
  model: string;
  /** Pin to this exact version (version page). */
  version?: string;
  /** Stages that have a version, for the model page's selector. */
  stages?: Stage[];
  artifacts?: Artifact[];
  format?: string;
}) {
  const info = useClientConfig();
  const [lang, setLang] = useState<Lang>("curl");
  const [stage, setStage] = useState<Stage>(stages?.includes("production") ? "production" : (stages?.[0] ?? "production"));
  if (!info) return null;
  const apiUrl = info.modelApi;
  const m = encodeURIComponent(model);
  const selectorQuery = version ? `version=${encodeURIComponent(version)}` : `stage=${stage}`;
  const selectorArg = version ? `version="${version}"` : `stage="${stage}"`;
  const selectorFlag = version ? `--version ${version}` : `--stage ${stage}`;
  const uri = version ? `lineage://${model}@${version}` : `lineage://${model}/${stage}`;
  const firstModel = artifacts?.find((a) => a.kind === "MODEL") ?? artifacts?.[0];

  const snippets: Record<Lang, { label?: string; code: string }[]> = {
    curl: [
      {
        label: "Find out what to fetch: the version, its files, digests and download links",
        code: `curl "${apiUrl}/v1/models/${m}/resolve?${selectorQuery}"`,
      },
      ...(version && firstModel
        ? [
            {
              label: `Download ${firstModel.name}`,
              code: `curl -L -o ${firstModel.name} \\\n  "${apiUrl}/v1/models/${m}/versions/${encodeURIComponent(version)}/artifacts/${encodeURIComponent(firstModel.name)}/content"`,
            },
          ]
        : []),
    ],
    python: [
      { label: "Install", code: "pip install lineage-sdk" },
      {
        code: `from lineage import Client\n\nclient = Client("${apiUrl}")\nfiles = client.download("${model}", ${selectorArg}, dest="./model")\nprint(files)`,
      },
    ],
    cli: [
      { code: `export LINEAGE_SERVER=${apiUrl}\nlineage resolve ${model} ${selectorFlag}\nlineage pull ${model} ${selectorFlag} --dest ./model` },
    ],
    kserve: [
      {
        label: "InferenceService — needs the lineage storage initializer installed once per cluster (see the docs)",
        code: `apiVersion: serving.kserve.io/v1beta1\nkind: InferenceService\nmetadata:\n  name: ${model}\nspec:\n  predictor:\n    model:\n      modelFormat:\n        name: ${format || "<format>"}\n      storageUri: ${uri}`,
      },
    ],
  };

  return (
    <section className="rounded-lg border bg-card">
      <div className="border-b px-5 py-4">
        <h2 className="text-[0.9375rem] font-semibold">{version ? "Use this version" : "Use this model"}</h2>
        <p className="mt-0.5 text-sm text-muted-foreground">
          {version
            ? `Pinned to ${version}: it will always fetch exactly these files.`
            : `Asking for the ${STAGE_LABEL[stage].toLowerCase()} stage means you pick up promotions automatically — no code change when a new version goes live.`}
        </p>
        {!version && stages && stages.length > 1 && (
          <div className="mt-3 flex flex-wrap gap-1.5">
            {stages.map((s) => (
              <button
                key={s}
                onClick={() => setStage(s)}
                className={cn(
                  "rounded-full border px-2.5 py-0.5 text-xs font-medium",
                  s === stage ? "border-brand bg-brand-soft text-brand" : "text-muted-foreground hover:text-foreground",
                )}
              >
                {STAGE_LABEL[s]}
              </button>
            ))}
          </div>
        )}
      </div>

      <div role="tablist" className="flex gap-1 overflow-x-auto overflow-y-hidden px-3 shadow-[inset_0_-1px_0_var(--color-border)]">
        {LANGS.map((l) => (
          <button
            key={l.id}
            role="tab"
            aria-selected={lang === l.id}
            onClick={() => setLang(l.id)}
            className={cn(
              "shrink-0 border-b-2 px-3 py-2.5 text-sm font-medium",
              lang === l.id ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {l.label}
          </button>
        ))}
      </div>

      <div className="space-y-4 px-5 py-4">
        {snippets[lang].map((s, i) => (
          <CopyBlock key={`${lang}${i}`} code={s.code} label={s.label} />
        ))}
        <p className="text-xs text-muted-foreground">
          Model API: <span className="font-mono">{apiUrl}</span>
          {info.guessed && " (assumed — set LINEAGE_PUBLIC_MODEL_API_URL if clients reach it at a different address)"}.{" "}
          <a href={`${info.docs}`} target="_blank" rel="noreferrer" className="font-medium text-brand hover:underline">
            Documentation
          </a>
          {" · "}
          <a href={`${apiUrl}/v1/openapi.json`} target="_blank" rel="noreferrer" className="font-medium text-brand hover:underline">
            API reference
          </a>
        </p>
      </div>
    </section>
  );
}
