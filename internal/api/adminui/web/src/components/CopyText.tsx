import { useState } from "react";
import { Check, Copy } from "lucide-react";

/** A short value — a URL, an id — with a copy button beside it. */
export function CopyText({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={() =>
        navigator.clipboard?.writeText(text).then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        })
      }
      title="Copy"
      className="flex w-full items-center gap-2 rounded-md border bg-muted px-2 py-1.5 text-left font-mono text-[0.75rem] text-foreground hover:border-input"
    >
      <span className="min-w-0 flex-1 truncate">{text}</span>
      {copied ? <Check className="h-3.5 w-3.5 shrink-0 text-ok" /> : <Copy className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
    </button>
  );
}
