// Shared loading / error / empty states and the page header.

import { Link } from "react-router-dom";
import { AlertTriangle, ChevronRight } from "lucide-react";
import { Logo } from "@/components/Logo";

export function Loading({ label = "Loading" }: { label?: string }) {
  return (
    <div className="flex items-center gap-2.5 px-1 py-10 text-sm text-muted-foreground">
      <Logo size={10} className="animate-pulse" />
      {label}…
    </div>
  );
}

export function ErrorNote({ error }: { error: string }) {
  return (
    <div className="flex items-start gap-2.5 rounded-lg border border-danger/30 bg-danger-soft px-4 py-3 text-sm">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-danger" strokeWidth={2} />
      <div>
        <div className="font-medium text-danger">Couldn't load this</div>
        <div className="text-foreground/80">{error}</div>
      </div>
    </div>
  );
}

export function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
      {children}
    </div>
  );
}

export interface Crumb {
  label: string;
  to?: string;
}

export function PageHeader({
  title,
  sub,
  right,
  crumbs,
}: {
  title: React.ReactNode;
  sub?: React.ReactNode;
  right?: React.ReactNode;
  crumbs?: Crumb[];
}) {
  return (
    <div className="mb-6">
      {crumbs && crumbs.length > 0 && (
        <nav aria-label="Breadcrumb" className="mb-2 flex items-center gap-1 text-sm text-muted-foreground">
          {crumbs.map((c, i) => (
            <span key={i} className="flex items-center gap-1">
              {c.to ? (
                <Link to={c.to} className="hover:text-foreground">
                  {c.label}
                </Link>
              ) : (
                <span>{c.label}</span>
              )}
              {i < crumbs.length - 1 && <ChevronRight className="h-3.5 w-3.5" />}
            </span>
          ))}
        </nav>
      )}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
          {sub && <div className="mt-1 max-w-3xl text-[0.9375rem] text-muted-foreground">{sub}</div>}
        </div>
        {right}
      </div>
    </div>
  );
}

/** A section heading inside a page: title, one line of explanation, optional action. */
export function SectionHeader({
  title,
  sub,
  right,
}: {
  title: React.ReactNode;
  sub?: React.ReactNode;
  right?: React.ReactNode;
}) {
  return (
    <div className="mb-3 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 className="text-base font-semibold">{title}</h2>
        {sub && <p className="mt-0.5 text-sm text-muted-foreground">{sub}</p>}
      </div>
      {right}
    </div>
  );
}
