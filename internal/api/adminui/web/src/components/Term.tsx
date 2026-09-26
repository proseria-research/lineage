import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { explain, type Explanation } from "@/lib/explain";

// A governance term that explains itself. Hover (or focus) shows a card with what it means in
// plain words, why it matters, an example and what to do; clicking pins it open. The card is
// portalled so no table or overflow-hidden card can clip it. Terms with no explanation render
// their children unchanged.

const W = 320;

function Card({ e, style, onEnter, onLeave }: { e: Explanation; style: React.CSSProperties; onEnter: () => void; onLeave: () => void }) {
  return (
    <div
      role="tooltip"
      style={style}
      onMouseEnter={onEnter}
      onMouseLeave={onLeave}
      className="fixed z-[60] w-80 rounded-lg border bg-popover p-4 text-left text-sm font-normal normal-case text-popover-foreground shadow-xl"
    >
      <div className="font-semibold">{e.term}</div>
      <p className="mt-1 leading-relaxed">{e.plain}</p>
      {e.why && (
        <p className="mt-2 leading-relaxed">
          <span className="font-medium">Why it matters: </span>
          <span className="text-muted-foreground">{e.why}</span>
        </p>
      )}
      {e.example && (
        <p className="mt-2 leading-relaxed">
          <span className="font-medium">For example: </span>
          <span className="text-muted-foreground">{e.example}</span>
        </p>
      )}
      {e.next && (
        <p className="mt-2 rounded-md bg-brand-soft px-2.5 py-1.5 leading-relaxed text-brand">
          <span className="font-medium">What to do: </span>
          {e.next}
        </p>
      )}
      {e.source && <p className="mt-2 text-xs text-muted-foreground">{e.source}. Not legal advice.</p>}
    </div>
  );
}

export function Term({ k, children, className }: { k?: string | null; children: React.ReactNode; className?: string }) {
  const e = explain(k);
  const ref = useRef<HTMLSpanElement>(null);
  const [open, setOpen] = useState(false);
  const [pinned, setPinned] = useState(false);
  const [pos, setPos] = useState<React.CSSProperties>({});
  const timer = useRef<number | undefined>(undefined);

  const show = () => {
    window.clearTimeout(timer.current);
    setOpen(true);
  };
  const hide = () => {
    if (pinned) return;
    timer.current = window.setTimeout(() => setOpen(false), 120);
  };

  useLayoutEffect(() => {
    if (!open || !ref.current) return;
    const r = ref.current.getBoundingClientRect();
    const left = Math.min(Math.max(8, r.left + r.width / 2 - W / 2), window.innerWidth - W - 8);
    const below = r.bottom + 8;
    setPos(below + 260 > window.innerHeight && r.top > 280 ? { left, bottom: window.innerHeight - r.top + 8 } : { left, top: below });
  }, [open]);

  useEffect(() => {
    if (!pinned) return;
    const close = (ev: MouseEvent | KeyboardEvent) => {
      if (ev instanceof KeyboardEvent && ev.key !== "Escape") return;
      if (ev instanceof MouseEvent && ref.current?.contains(ev.target as Node)) return;
      setPinned(false);
      setOpen(false);
    };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", close);
    window.addEventListener("scroll", () => setOpen(false), { once: true, capture: true });
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", close);
    };
  }, [pinned]);

  if (!e) return <>{children}</>;
  return (
    <span
      ref={ref}
      tabIndex={0}
      role="button"
      aria-label={`${e.term}: what this means`}
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
      onClick={(ev) => {
        ev.preventDefault();
        ev.stopPropagation();
        setPinned((p) => !p);
        setOpen(true);
      }}
      className={`inline-flex cursor-help rounded-full outline-offset-2 ${className ?? ""}`}
    >
      {children}
      {open && createPortal(<Card e={e} style={pos} onEnter={show} onLeave={hide} />, document.body)}
    </span>
  );
}
