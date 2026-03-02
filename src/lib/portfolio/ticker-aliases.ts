/**
 * Broker exports can reference the same instrument with different symbols
 * across corporate actions (e.g. bankruptcy/relisting chains).
 */
const TICKER_ALIASES: Record<string, string> = {
  EXE: "CHK",
  CHKAQ: "CHK",
};

/**
 * Some delisted symbols never get an explicit zero-value closure row.
 * For those, we force-close any leftover quantity at 0 if there has been
 * no activity after the configured cutoff date.
 */
export const DELISTED_ZERO_CLOSE_CUTOFF: Record<string, string> = {
  CHK: "2020-12-31",
};

export function normalizeTicker(ticker: string): string {
  return TICKER_ALIASES[ticker] ?? ticker;
}

/**
 * Return all raw tickers that map to the given canonical ticker.
 */
export function getRawTickersForCanonical(canonical: string): string[] {
  const result = [canonical];
  for (const [raw, target] of Object.entries(TICKER_ALIASES)) {
    if (target === canonical && !result.includes(raw)) {
      result.push(raw);
    }
  }
  return result;
}
