// Small shared presentational helpers: loading / error / empty states + page header.

import { Logo } from "@/components/Logo";

export function Loading({ label = "Loading" }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 px-1 py-8">
      <Logo size={10} className="animate-pulse" />
      <span className="label-caps">{label}…</span>
    </div>
  );
}

export function ErrorNote({ error }: { error: string }) {
  return (
    <div className="border border-destructive/50 bg-muted px-3 py-2 text-sm">
      <span className="label-caps mr-2 text-destructive">Error</span>
      {error}
    </div>
  );
}

export function Empty({ children }: { children: React.ReactNode }) {
  return <div className="border border-dashed px-3 py-8 text-center text-sm text-muted-foreground">{children}</div>;
}

export function PageHeader({ title, sub, right }: { title: React.ReactNode; sub?: string; right?: React.ReactNode }) {
  return (
    <div className="mb-5 flex items-end justify-between gap-4 border-b pb-3">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">{title}</h1>
        {sub && <div className="mt-0.5 text-sm text-muted-foreground">{sub}</div>}
      </div>
      {right}
    </div>
  );
}
