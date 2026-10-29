// Exact decimal arithmetic for prices, so a cost here is the same number the
// Python catalog computes with Decimal. A value is n / 10^s.

export interface Dec {
  n: bigint;
  s: number;
}

const DECIMAL = /^([+-]?)(\d*)(?:\.(\d*))?(?:e([+-]?\d+))?$/i;

/** Parse a decimal string or a finite number. Null when it is not one. */
export function dec(value: unknown): Dec | null {
  if (value === null || value === undefined || typeof value === "boolean") return null;
  if (typeof value === "number" && !Number.isFinite(value)) return null;
  if (typeof value !== "number" && typeof value !== "string" && typeof value !== "bigint") return null;
  const match = DECIMAL.exec(String(value).trim());
  if (!match) return null;
  const [, sign = "", whole = "", fraction = "", exponent = "0"] = match;
  if (!whole && !fraction) return null;
  let n = BigInt((whole || "0") + fraction);
  let s = fraction.length - Number(exponent);
  if (s < 0) {
    n *= 10n ** BigInt(-s);
    s = 0;
  }
  return { n: sign === "-" ? -n : n, s };
}

export const ZERO: Dec = { n: 0n, s: 0 };
export const ONE: Dec = { n: 1n, s: 0 };

export function int(value: number | bigint): Dec {
  return { n: BigInt(value), s: 0 };
}

export function mul(a: Dec, b: Dec): Dec {
  return { n: a.n * b.n, s: a.s + b.s };
}

export function add(a: Dec, b: Dec): Dec {
  if (a.s === b.s) return { n: a.n + b.n, s: a.s };
  if (a.s < b.s) return { n: a.n * 10n ** BigInt(b.s - a.s) + b.n, s: b.s };
  return { n: a.n + b.n * 10n ** BigInt(a.s - b.s), s: a.s };
}

/** Divide by 10^places, exactly. */
export function shift(a: Dec, places: number): Dec {
  return { n: a.n, s: a.s + places };
}

/** Units of 10^-places, rounded half away from zero (Decimal ROUND_HALF_UP). */
export function unitsHalfUp(a: Dec, places: number): bigint {
  if (a.s <= places) return a.n * 10n ** BigInt(places - a.s);
  const divisor = 10n ** BigInt(a.s - places);
  const negative = a.n < 0n;
  const magnitude = negative ? -a.n : a.n;
  let quotient = magnitude / divisor;
  if ((magnitude % divisor) * 2n >= divisor) quotient += 1n;
  return negative ? -quotient : quotient;
}

/** Units of 10^-places, rounded away from zero (Decimal ROUND_UP). */
export function unitsUp(a: Dec, places: number): bigint {
  if (a.s <= places) return a.n * 10n ** BigInt(places - a.s);
  const divisor = 10n ** BigInt(a.s - places);
  const negative = a.n < 0n;
  const magnitude = negative ? -a.n : a.n;
  let quotient = magnitude / divisor;
  if (magnitude % divisor !== 0n) quotient += 1n;
  return negative ? -quotient : quotient;
}

/** A count of 10^-places units as a number (exact below 2^53 units). */
export function unitsToNumber(units: bigint, places: number): number {
  return Number(units) / 10 ** places;
}

/** A number that was a count of 10^-places units back to that count. */
export function numberToUnits(value: number, places: number): bigint {
  return BigInt(Math.round(value * 10 ** places));
}

export function compare(a: Dec, b: Dec): number {
  const difference = add(a, { n: -b.n, s: b.s }).n;
  return difference < 0n ? -1 : difference > 0n ? 1 : 0;
}
