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
    const sign = match[1] ?? "";
    const whole = match[2] ?? "0";
    const fraction = match[3] ? `${decimal}${match[3]}` : "";
    const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, group);
    return `${currency.toUpperCase()} ${sign}${grouped}${fraction}`;
  } catch {
    return `${amount} ${currency}`;
  }
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
