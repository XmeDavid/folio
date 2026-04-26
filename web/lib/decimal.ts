// Decimal-string utilities. Folio never converts amounts to floats — the
// backend owns arithmetic. These helpers are for display-side aggregation
// (e.g. summing balances per currency) and form validation.

export const AMOUNT_REGEX = /^-?\d+(\.\d+)?$/;

export function parseDecimal(value: string): { units: bigint; scale: number } {
  const match = value.trim().match(/^([+-]?)(\d+)(?:\.(\d+))?$/);
  if (!match) return { units: 0n, scale: 0 };
  const fraction = match[3] ?? "";
  const units = BigInt(`${match[1] ?? ""}${match[2]}${fraction}`);
  return { units, scale: fraction.length };
}

export function formatDecimal(units: bigint, scale: number): string {
  const sign = units < 0n ? "-" : "";
  const raw = (units < 0n ? -units : units).toString().padStart(scale + 1, "0");
  if (scale === 0) return `${sign}${raw}`;
  const whole = raw.slice(0, -scale);
  const fraction = raw.slice(-scale).replace(/0+$/, "");
  return fraction ? `${sign}${whole}.${fraction}` : `${sign}${whole}`;
}

export function addDecimalStrings(a: string, b: string): string {
  const left = parseDecimal(a);
  const right = parseDecimal(b);
  const scale = Math.max(left.scale, right.scale);
  const total =
    left.units * 10n ** BigInt(scale - left.scale) +
    right.units * 10n ** BigInt(scale - right.scale);
  return formatDecimal(total, scale);
}

export function multiplyDecimalStrings(a: string, b: string): string {
  const left = parseDecimal(a);
  const right = parseDecimal(b);
  return formatDecimal(left.units * right.units, left.scale + right.scale);
}
