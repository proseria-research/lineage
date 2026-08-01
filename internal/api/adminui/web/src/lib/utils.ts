import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function fmtBytes(n?: number): string {
  if (!n) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v % 1 === 0 ? v : v.toFixed(1)} ${units[i]}`;
}

export function fmtTime(ms?: number): string {
  if (!ms) return "—";
  const d = new Date(ms);
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function relTime(ms?: number): string {
  if (!ms) return "—";
  const delta = Date.now() - ms;
  const s = Math.floor(delta / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// Parameter counts read better abbreviated: 8_030_261_248 -> "8.03B". Zero is a real
// value here, so only null/undefined render as "not reported" (§11.2).
export function fmtCount(n?: number | null): string {
  if (n === null || n === undefined) return NOT_REPORTED;
  if (n < 1000) return String(n);
  const units: [number, string][] = [
    [1e12, "T"],
    [1e9, "B"],
    [1e6, "M"],
    [1e3, "K"],
  ];
  for (const [scale, suffix] of units) {
    if (n >= scale) return `${(n / scale).toFixed(2).replace(/\.?0+$/, "")}${suffix}`;
  }
  return String(n);
}

// A fact nobody submitted is not a zero and not a blank. It renders as its own thing so a
// reader can tell "nothing reported this" from "reported as none" (§11.8).
export const NOT_REPORTED = "not reported";

// fmtBytes returns "—" for absent values; insight surfaces want the explicit wording.
export function fmtBytesOrUnreported(n?: number | null): string {
  return n === null || n === undefined ? NOT_REPORTED : fmtBytes(n);
}

// Signed byte delta, e.g. "-9.6 GB". Zero is "no change" rather than "0 B".
export function fmtDeltaBytes(n?: number | null): string {
  if (n === null || n === undefined) return "—";
  if (n === 0) return "no change";
  return `${n > 0 ? "+" : "−"}${fmtBytes(Math.abs(n))}`;
}

export function shortDigest(d?: string): string {
  if (!d) return "—";
  const [algo, hex] = d.split(":");
  if (!hex) return d;
  return `${algo}:${hex.slice(0, 12)}`;
}
