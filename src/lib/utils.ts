import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatMoney(
  amount: number,
  currency: string = "CHF"
): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(amount);
}

export function formatPercent(value: number): string {
  const sign = value >= 0 ? "+" : "";
  return `${sign}${value.toFixed(2)}%`;
}

export function formatQuantity(qty: number): string {
  if (Number.isInteger(qty)) return qty.toString();
  if (qty < 1) return qty.toFixed(6);
  return qty.toFixed(4);
}

export function pnlColor(value: number): string {
  if (value > 0) return "text-green";
  if (value < 0) return "text-red";
  return "text-text-secondary";
}

export function pnlBg(value: number): string {
  if (value > 0) return "bg-green-dim";
  if (value < 0) return "bg-red-dim";
  return "bg-bg-tertiary";
}
