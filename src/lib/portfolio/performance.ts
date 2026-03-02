import { getActivePositions, getAccountLevelFees, type Position } from "./positions";
import { fetchBulkCurrentPrices } from "../prices/yahoo";
import { getCurrentFxRate } from "../fx/convert";
import type { Currency } from "../fx/convert";

export interface HoldingWithPrice extends Position {
  currentPrice: number | null;
  currentValue: number;
  unrealizedPnL: number;
  unrealizedPnLPercent: number;
  totalReturn: number;
  totalReturnPercent: number;
}

export interface PortfolioSummary {
  currency: Currency;
  fxRate: number;
  totalCurrentValue: number;
  totalCostBasis: number;
  totalUnrealizedPnL: number;
  totalUnrealizedPnLPercent: number;
  totalRealizedPnL: number;
  totalDividends: number;
  totalCommissions: number;
  totalReturn: number;
  totalReturnPercent: number;
  holdings: HoldingWithPrice[];
}

export async function getPortfolioSummary(
  displayCurrency: Currency = "CHF",
  accountId?: string
): Promise<PortfolioSummary> {
  const positions = await getActivePositions({ accountId });
  const tickers = [...new Set(positions.map((p) => p.ticker))];
  const preferredCurrencies = new Map<string, string>();
  for (const p of positions) {
    if (!preferredCurrencies.has(p.ticker)) {
      preferredCurrencies.set(p.ticker, p.currency);
    }
  }
  const prices = await fetchBulkCurrentPrices(tickers, preferredCurrencies);

  const fxRates: Record<string, number> = {};
  const currencies = [...new Set(positions.map((p) => p.currency as Currency))];
  for (const cur of currencies) {
    if (cur !== displayCurrency) {
      fxRates[cur] = await getCurrentFxRate(cur, displayCurrency);
    } else {
      fxRates[cur] = 1;
    }
  }

  const fxToDisplay = fxRates["USD"] || 1;

  const holdings: HoldingWithPrice[] = positions.map((pos) => {
    const priceData = prices.get(pos.ticker);
    const currentPrice = priceData?.price ?? null;
    const fx = fxRates[pos.currency] || 1;

    const currentValueOriginal = currentPrice ? currentPrice * pos.quantity : 0;
    const costBasisTotal = pos.avgCostBasis * pos.quantity;

    const currentValue = currentValueOriginal * fx;
    const costInDisplay = costBasisTotal * fx;
    const unrealizedPnL = currentValue - costInDisplay;
    const unrealizedPnLPercent = costInDisplay > 0 ? (unrealizedPnL / costInDisplay) * 100 : 0;

    const dividendsInDisplay = pos.dividendsReceived * fx;
    const realizedInDisplay = pos.realizedPnL * fx;
    const commissionsInDisplay = pos.commissionsTotal * fx;

    const totalReturn =
      unrealizedPnL + realizedInDisplay + dividendsInDisplay - commissionsInDisplay;
    const totalReturnPercent =
      pos.totalInvested * fx > 0 ? (totalReturn / (pos.totalInvested * fx)) * 100 : 0;

    return {
      ...pos,
      currentPrice,
      currentValue: Math.round(currentValue * 100) / 100,
      unrealizedPnL: Math.round(unrealizedPnL * 100) / 100,
      unrealizedPnLPercent: Math.round(unrealizedPnLPercent * 100) / 100,
      totalReturn: Math.round(totalReturn * 100) / 100,
      totalReturnPercent: Math.round(totalReturnPercent * 100) / 100,
    };
  });

  holdings.sort((a, b) => Math.abs(b.currentValue) - Math.abs(a.currentValue));

  const totalCurrentValue = holdings.reduce((s, h) => s + h.currentValue, 0);
  const totalCostBasis = holdings.reduce(
    (s, h) => s + h.avgCostBasis * h.quantity * (fxRates[h.currency] || 1),
    0
  );
  const totalUnrealizedPnL = totalCurrentValue - totalCostBasis;
  const totalRealizedPnL = holdings.reduce(
    (s, h) => s + h.realizedPnL * (fxRates[h.currency] || 1),
    0
  );
  const totalDividends = holdings.reduce(
    (s, h) => s + h.dividendsReceived * (fxRates[h.currency] || 1),
    0
  );
  let totalCommissions = holdings.reduce(
    (s, h) => s + h.commissionsTotal * (fxRates[h.currency] || 1),
    0
  );

  const acctFees = await getAccountLevelFees({ accountId });
  for (const fee of acctFees) {
    const cur = fee.currency as Currency;
    if (cur !== displayCurrency && !fxRates[cur]) {
      fxRates[cur] = await getCurrentFxRate(cur, displayCurrency);
    }
    totalCommissions += fee.amount * (fxRates[cur] || 1);
  }

  const totalReturn = totalUnrealizedPnL + totalRealizedPnL + totalDividends - totalCommissions;

  return {
    currency: displayCurrency,
    fxRate: fxToDisplay,
    totalCurrentValue: Math.round(totalCurrentValue * 100) / 100,
    totalCostBasis: Math.round(totalCostBasis * 100) / 100,
    totalUnrealizedPnL: Math.round(totalUnrealizedPnL * 100) / 100,
    totalUnrealizedPnLPercent:
      totalCostBasis > 0
        ? Math.round((totalUnrealizedPnL / totalCostBasis) * 10000) / 100
        : 0,
    totalRealizedPnL: Math.round(totalRealizedPnL * 100) / 100,
    totalDividends: Math.round(totalDividends * 100) / 100,
    totalCommissions: Math.round(totalCommissions * 100) / 100,
    totalReturn: Math.round(totalReturn * 100) / 100,
    totalReturnPercent:
      totalCostBasis > 0
        ? Math.round((totalReturn / totalCostBasis) * 10000) / 100
        : 0,
    holdings,
  };
}
