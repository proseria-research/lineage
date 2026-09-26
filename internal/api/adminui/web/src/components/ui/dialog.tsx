import { useEffect } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";

/** A centred modal with a title, a body and a footer. Escape and a backdrop click close it. */
export function Dialog({
  title,
  sub,
  onClose,
  children,
  footer,
}: {
  title: React.ReactNode;
  sub?: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
  footer: React.ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-foreground/20 p-4 backdrop-blur-sm sm:p-10"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div role="dialog" aria-modal="true" className="w-full max-w-xl rounded-lg border bg-card shadow-xl">
        <div className="border-b px-5 py-4">
          <div className="text-base font-semibold">{title}</div>
          {sub && <div className="mt-0.5 text-sm text-muted-foreground">{sub}</div>}
        </div>
        <div className="space-y-4 px-5 py-4">{children}</div>
        <div className="flex items-center justify-end gap-2 border-t px-5 py-3">{footer}</div>
      </div>
    </div>
  );
}

export function DialogFooter({
  busy,
  blocked,
  onCancel,
  onSave,
  saveLabel,
}: {
  busy: boolean;
  blocked?: boolean;
  onCancel: () => void;
  onSave: () => void;
  saveLabel: string;
}) {
  return (
    <>
      <Button variant="outline" size="sm" onClick={onCancel} disabled={busy}>
        Cancel
      </Button>
      <Button size="sm" onClick={onSave} disabled={busy || blocked}>
        {busy ? "Saving…" : saveLabel}
      </Button>
    </>
  );
}

export function Field({
  label,
  hint,
  error,
  required,
  children,
}: {
  label: string;
  hint?: React.ReactNode;
  error?: React.ReactNode;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-sm font-medium">
        {label}
        {required && <span className="ml-1.5 text-xs font-normal text-muted-foreground">required</span>}
      </span>
      {children}
      {error ? (
        <span className="text-xs text-danger">{error}</span>
      ) : hint ? (
        <span className="text-xs text-muted-foreground">{hint}</span>
      ) : null}
    </label>
  );
}

export function FormError({ error }: { error: string }) {
  if (!error) return null;
  return (
    <div className="flex items-start gap-2 rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-sm">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
      {error}
    </div>
  );
}

export const controlClass =
  "rounded-md border border-input bg-card px-3 py-2 text-sm outline-none focus-visible:border-ring";

/** Date input ⇄ epoch millis. */
export const toDateInput = (ms?: number) => (ms ? new Date(ms).toISOString().slice(0, 10) : "");
export const fromDateInput = (s: string) => (s ? Date.parse(`${s}T00:00:00Z`) : null);
