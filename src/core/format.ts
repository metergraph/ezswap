// Formatters for the figures the report prints. They match Metergraph's
// hosted figures, pinned by conformance/experiment_equivalence.json. A bare
// "—" marks a missing value.

export const MISSING_VALUE = "—";

function finite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

export function formatUsd(value: number | null | undefined): string {
  if (!finite(value)) return MISSING_VALUE;
  if (value === 0) return "$0";
  if (value >= 100) return `$${Math.round(value).toLocaleString("en-US")}`;
  if (value >= 1) return `$${value.toFixed(2)}`;
  if (value >= 0.01) return `$${value.toFixed(3)}`;
  return `$${Number(value.toPrecision(2)).toString()}`;
}

export function formatMs(value: number | null | undefined): string {
  if (!finite(value)) return MISSING_VALUE;
  if (value >= 10000) return `${(value / 1000).toFixed(0)} s`;
  if (value >= 1000) return `${(value / 1000).toFixed(1)} s`;
  return `${Math.round(value)} ms`;
}

export function formatPercent(value: number | null | undefined): string {
  if (!finite(value)) return MISSING_VALUE;
  return `${Math.round(value * 100)}%`;
}

export function formatCount(value: number | null | undefined): string {
  if (!finite(value)) return MISSING_VALUE;
  return Math.round(value).toLocaleString("en-US");
}

export function formatNumber(value: number | null | undefined): string {
  if (!finite(value)) return MISSING_VALUE;
  if (Number.isInteger(value)) return value.toLocaleString("en-US");
  const abs = Math.abs(value);
  return value.toFixed(abs >= 100 ? 0 : abs >= 10 ? 1 : 2);
}

/** A signed figure with a true minus sign (U+2212). */
export function formatSigned(value: number | null | undefined, format: (v: number) => string = formatNumber): string {
  if (!finite(value)) return MISSING_VALUE;
  if (value === 0) return format(0);
  return `${value > 0 ? "+" : "−"}${format(Math.abs(value))}`;
}

/** A call whose reply carried no cache count is not reported, never 0%. */
export function formatCachedShare(value: number | null | undefined): string {
  return !finite(value) ? "not reported" : formatPercent(value);
}
