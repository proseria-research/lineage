// The Lineage mark: a solid square. Monochrome and sharp — it inherits the current text
// color (via currentColor), so it flips correctly in light/dark. Used as the brand mark and
// as an accent in loading/empty states.

export function Logo({ size = 12, className = "" }: { size?: number; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={className}
      style={{ display: "inline-block", width: size, height: size, background: "currentColor" }}
    />
  );
}

// Wordmark is the mark plus the LINEAGE lettering — the standard brand lockup.
export function Wordmark({ size = 12, className = "" }: { size?: number; className?: string }) {
  return (
    <span className={`flex items-center gap-2.5 ${className}`}>
      <Logo size={size} />
      <span className="text-sm font-semibold tracking-[0.18em]">LINEAGE</span>
    </span>
  );
}
