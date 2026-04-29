// Display-only formatters. Never convert these back into floats for math;
// the backend owns all arithmetic and returns decimal strings.

export function formatAmount(
  amount: string | null | undefined,
  currency: string,
  locale?: string
): string {
  if (amount == null || amount === "") return "-";

  const match = amount.trim().match(/^([+-]?)(\d+)(?:\.(\d+))?$/);
  if (!match) return `${amount} ${currency}`;

  try {
    const parts = new Intl.NumberFormat(locale).formatToParts(12345.6);
    const group = parts.find((part) => part.type === "group")?.value ?? ",";
    const decimal = parts.find((part) => part.type === "decimal")?.value ?? ".";
    const ccy = currency.toUpperCase();
    const sign = match[1] ?? "";
    const rounded = formatFraction(match[2] ?? "0", match[3] ?? "", ccy);
    const grouped = rounded.whole.replace(/\B(?=(\d{3})+(?!\d))/g, group);
    const fraction = rounded.fraction ? `${decimal}${rounded.fraction}` : "";
    return `${ccy} ${sign}${grouped}${fraction}`;
  } catch {
    return `${amount} ${currency}`;
  }
}

export function formatQuantity(
  quantity: string | number | null | undefined,
  opts: { maxFractionDigits?: number; locale?: string } = {}
): string {
  if (quantity == null || quantity === "") return "-";
  const text = String(quantity).trim();
  const match = text.match(/^([+-]?)(\d+)(?:\.(\d+))?$/);
  if (!match) return text;

  const maxFractionDigits = opts.maxFractionDigits ?? 4;
  const rounded = roundToFixed(
    match[2] ?? "0",
    match[3] ?? "",
    maxFractionDigits
  );
  const fraction = rounded.fraction.replace(/0+$/, "");
  try {
    const parts = new Intl.NumberFormat(opts.locale).formatToParts(12345.6);
    const group = parts.find((part) => part.type === "group")?.value ?? ",";
    const decimal = parts.find((part) => part.type === "decimal")?.value ?? ".";
    const grouped = rounded.whole.replace(/\B(?=(\d{3})+(?!\d))/g, group);
    return `${match[1] ?? ""}${grouped}${fraction ? `${decimal}${fraction}` : ""}`;
  } catch {
    return `${match[1] ?? ""}${rounded.whole}${fraction ? `.${fraction}` : ""}`;
  }
}

const minorUnitsByCurrency = new Map<string, number>([
  ["BHD", 3],
  ["CLP", 0],
  ["AUD", 2],
  ["BRL", 2],
  ["CAD", 2],
  ["CHF", 2],
  ["CZK", 2],
  ["DKK", 2],
  ["EUR", 2],
  ["GBP", 2],
  ["HKD", 2],
  ["HUF", 2],
  ["JPY", 0],
  ["KWD", 3],
  ["MXN", 2],
  ["NZD", 2],
  ["NOK", 2],
  ["PHP", 2],
  ["PLN", 2],
  ["RON", 2],
  ["SEK", 2],
  ["SGD", 2],
  ["USD", 2],
  ["ZAR", 2],
]);

function formatFraction(
  whole: string,
  fraction: string,
  currency: string
): { whole: string; fraction: string } {
  const minorUnits = minorUnitsByCurrency.get(currency);
  if (minorUnits != null) {
    return roundToFixed(whole, fraction, minorUnits);
  }

  const trimmed = fraction.replace(/0+$/, "");
  return { whole, fraction: trimmed };
}

function roundToFixed(
  whole: string,
  fraction: string,
  digits: number
): { whole: string; fraction: string } {
  if (digits === 0) {
    const increment = Number(fraction[0] ?? "0") >= 5 ? 1n : 0n;
    return { whole: (BigInt(whole) + increment).toString(), fraction: "" };
  }

  if (fraction.length <= digits) {
    return { whole, fraction: fraction.padEnd(digits, "0") };
  }

  const kept = fraction.slice(0, digits);
  const next = Number(fraction[digits] ?? "0");
  let scaled = BigInt(`${whole}${kept}`);
  if (next >= 5) {
    scaled += 1n;
  }

  const scaledText = scaled.toString().padStart(digits + 1, "0");
  return {
    whole: scaledText.slice(0, -digits),
    fraction: scaledText.slice(-digits),
  };
}

export function formatDate(value: string | null | undefined, locale?: string) {
  if (!value) return "-";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "short",
    day: "2-digit",
  }).format(d);
}

export function formatDateTime(
  value: string | null | undefined,
  locale?: string
) {
  if (!value) return "-";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(d);
}
