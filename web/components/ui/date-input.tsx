"use client";

import * as React from "react";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

// DateInput renders an EU-style dd/mm/yyyy text input that emits an ISO
// "YYYY-MM-DD" string. Chrome ignores <html lang> for native <input type=
// "date">, which forces mm/dd/yyyy on en locales — so the app uses this
// component everywhere a fixed dd/mm/yyyy display is desired.
export function DateInput({
  value,
  onChange,
  className,
  id,
  placeholder = "dd/mm/yyyy",
  required,
  disabled,
}: {
  value: string; // YYYY-MM-DD (or "")
  onChange: (iso: string) => void;
  className?: string;
  id?: string;
  placeholder?: string;
  required?: boolean;
  disabled?: boolean;
}) {
  // Track local edit text plus the last known external value. When the
  // parent updates `value` we re-derive the displayed text. This is the
  // React-blessed "adjusting state when a prop changes" pattern: cheaper
  // than useEffect, and lint-clean.
  const [text, setText] = React.useState<string>(() => isoToDisplay(value));
  const [lastValue, setLastValue] = React.useState<string>(value);
  if (lastValue !== value) {
    setLastValue(value);
    setText(isoToDisplay(value));
  }

  return (
    <Input
      id={id}
      type="text"
      inputMode="numeric"
      autoComplete="off"
      placeholder={placeholder}
      pattern="\d{2}/\d{2}/\d{4}"
      maxLength={10}
      value={text}
      required={required}
      disabled={disabled}
      onChange={(e) => {
        const masked = applyMask(e.target.value);
        setText(masked);
        const iso = displayToIso(masked);
        if (iso !== null) onChange(iso);
        else if (masked === "") onChange("");
      }}
      onBlur={() => {
        const iso = displayToIso(text);
        if (iso === null && text !== "") {
          // Reject malformed input on blur, snapping back to the last valid
          // value (or empty when there isn't one).
          setText(isoToDisplay(value));
        }
      }}
      className={cn("font-mono tabular-nums", className)}
    />
  );
}

function isoToDisplay(iso: string): string {
  if (!iso) return "";
  const m = iso.match(/^(\d{4})-(\d{2})-(\d{2})/);
  if (!m) return "";
  return `${m[3]}/${m[2]}/${m[1]}`;
}

function displayToIso(display: string): string | null {
  const m = display.match(/^(\d{2})\/(\d{2})\/(\d{4})$/);
  if (!m) return null;
  const [, dd, mm, yyyy] = m;
  const day = Number(dd);
  const month = Number(mm);
  const year = Number(yyyy);
  if (
    !Number.isFinite(day) ||
    !Number.isFinite(month) ||
    !Number.isFinite(year)
  ) {
    return null;
  }
  if (month < 1 || month > 12 || day < 1 || day > 31 || year < 1900) {
    return null;
  }
  // Final sanity: round-trip through Date.
  const probe = new Date(`${yyyy}-${mm}-${dd}T00:00:00Z`);
  if (Number.isNaN(probe.getTime())) return null;
  if (
    probe.getUTCFullYear() !== year ||
    probe.getUTCMonth() + 1 !== month ||
    probe.getUTCDate() !== day
  ) {
    return null; // e.g. 31/02/2026
  }
  return `${yyyy}-${mm}-${dd}`;
}

// applyMask inserts slashes as the user types, accepting digits only and
// keeping the input within "dd/mm/yyyy".
function applyMask(raw: string): string {
  const digits = raw.replace(/\D+/g, "").slice(0, 8);
  if (digits.length <= 2) return digits;
  if (digits.length <= 4) return `${digits.slice(0, 2)}/${digits.slice(2)}`;
  return `${digits.slice(0, 2)}/${digits.slice(2, 4)}/${digits.slice(4)}`;
}
