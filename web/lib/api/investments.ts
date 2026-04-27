// Folio investments API surface. Mirrors backend/internal/investments shapes.
// Workspace-scoped routes live under /api/v1/t/{workspaceId}/investments/*.

const CSRF_HEADER_NAME = "X-Folio-Request";
const CSRF_HEADER_VALUE = "1";

const baseUrl =
  typeof window === "undefined"
    ? (process.env.API_URL ?? "http://localhost:8080")
    : ""; // browser uses Next rewrite

class ApiError extends Error {
  status: number;
  body: unknown;
  constructor(status: number, body: unknown) {
    const msg =
      typeof body === "object" && body !== null && "error" in body
        ? String((body as { error: string }).error)
        : `Request failed (${status})`;
    super(msg);
    this.status = status;
    this.body = body;
  }
}

async function request<T>(
  path: string,
  init: RequestInit & { json?: unknown } = {}
): Promise<T> {
  const { json, headers, ...rest } = init;
  const merged: Record<string, string> = {
    [CSRF_HEADER_NAME]: CSRF_HEADER_VALUE,
    ...((headers as Record<string, string> | undefined) ?? {}),
  };
  let body = rest.body;
  if (json !== undefined) {
    merged["Content-Type"] = "application/json";
    body = JSON.stringify(json);
  }
  const res = await fetch(`${baseUrl}${path}`, {
    ...rest,
    credentials: "include",
    headers: merged,
    body,
  });
  if (!res.ok) {
    let parsed: unknown;
    try {
      parsed = await res.json();
    } catch {
      parsed = undefined;
    }
    throw new ApiError(res.status, parsed);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type Instrument = {
  id: string;
  symbol: string;
  isin?: string | null;
  name: string;
  assetClass: string;
  currency: string;
  exchange?: string | null;
  active: boolean;
  createdAt: string;
  updatedAt: string;
};

export type Trade = {
  id: string;
  workspaceId: string;
  accountId: string;
  instrumentId: string;
  symbol: string;
  side: "buy" | "sell";
  quantity: string;
  price: string;
  currency: string;
  feeAmount: string;
  feeCurrency: string;
  tradeDate: string;
  settleDate?: string | null;
  linkedCashTransactionId?: string | null;
  createdAt: string;
  updatedAt: string;
};

export type DividendEvent = {
  id: string;
  workspaceId: string;
  accountId: string;
  instrumentId: string;
  symbol: string;
  exDate: string;
  payDate: string;
  amountPerUnit: string;
  currency: string;
  totalAmount: string;
  taxWithheld: string;
  linkedCashTransactionId?: string | null;
  createdAt: string;
};

export type Position = {
  accountId: string;
  instrumentId: string;
  workspaceId: string;
  symbol: string;
  name: string;
  assetClass: string;
  instrumentCurrency: string;
  accountCurrency: string;
  quantity: string;
  averageCost: string;
  costBasisTotal: string;
  realisedPnL: string;
  dividendsReceived: string;
  feesPaid: string;
  lastTradeDate?: string | null;
  lastPrice?: string | null;
  lastPriceAt?: string | null;
  marketValue?: string | null;
  unrealisedPnL?: string | null;
};

export type Holding = Position & {
  reportCurrency: string;
  fxRate: string;
  marketValueReport?: string | null;
  costBasisReport: string;
  unrealisedPnLReport?: string | null;
  realisedPnLReport: string;
  dividendsReport: string;
  feesReport: string;
  totalReturnReport?: string | null;
  totalReturnPercentReport?: string | null;
};

export type AllocationSlice = {
  key: string;
  label: string;
  value: string;
  pct: string;
};

export type HoldingMover = {
  symbol: string;
  name: string;
  unrealisedPnL: string;
  unrealisedPct: string;
  reportCurrency: string;
};

export type DashboardSummary = {
  reportCurrency: string;
  generatedAt: string;
  totalMarketValue: string;
  totalCostBasis: string;
  totalUnrealisedPnL: string;
  totalUnrealisedPnLPct: string;
  totalRealisedPnL: string;
  totalDividends: string;
  totalFees: string;
  totalReturn: string;
  totalReturnPct: string;
  openPositionsCount: number;
  staleQuotes: number;
  missingQuotes: number;
  holdings: Holding[];
  allocationByCurrency: AllocationSlice[];
  allocationByAccount: AllocationSlice[];
  allocationByAssetClass: AllocationSlice[];
  topMovers: HoldingMover[];
  warnings?: string[];
};

export type HistoryDataPoint = {
  date: string;
  quantity: string;
  price?: string | null;
  value?: string | null;
};

export type QuoteSnapshot = {
  price: string;
  currency: string;
  asOf: string;
  source: string;
  stale: boolean;
};

export type InstrumentDetail = {
  instrument: Instrument;
  positions: Position[];
  trades: Trade[];
  dividends: DividendEvent[];
  history: HistoryDataPoint[];
  lastQuote?: QuoteSnapshot | null;
};

// ---------------------------------------------------------------------------
// API
// ---------------------------------------------------------------------------

const root = (workspaceId: string) =>
  `/api/v1/t/${workspaceId}/investments`;

export async function fetchDashboard(
  workspaceId: string,
  opts: { accountId?: string; currency?: string } = {}
): Promise<DashboardSummary> {
  const qs: string[] = [];
  if (opts.accountId) qs.push(`accountId=${encodeURIComponent(opts.accountId)}`);
  if (opts.currency) qs.push(`currency=${encodeURIComponent(opts.currency)}`);
  const suffix = qs.length ? `?${qs.join("&")}` : "";
  return request<DashboardSummary>(`${root(workspaceId)}/dashboard${suffix}`);
}

export async function fetchPositions(
  workspaceId: string,
  opts: { accountId?: string; status?: "open" | "closed"; search?: string } = {}
): Promise<Position[]> {
  const qs: string[] = [];
  if (opts.accountId) qs.push(`accountId=${encodeURIComponent(opts.accountId)}`);
  if (opts.status) qs.push(`status=${opts.status}`);
  if (opts.search) qs.push(`search=${encodeURIComponent(opts.search)}`);
  const suffix = qs.length ? `?${qs.join("&")}` : "";
  return request<Position[]>(`${root(workspaceId)}/positions${suffix}`);
}

export async function fetchInstrumentDetail(
  workspaceId: string,
  instrumentIdOrSymbol: string
): Promise<InstrumentDetail> {
  return request<InstrumentDetail>(
    `${root(workspaceId)}/instruments/${encodeURIComponent(instrumentIdOrSymbol)}`
  );
}

export async function searchInstruments(
  workspaceId: string,
  q: string
): Promise<Instrument[]> {
  return request<Instrument[]>(
    `${root(workspaceId)}/instruments?q=${encodeURIComponent(q)}`
  );
}

export async function refreshInvestments(
  workspaceId: string
): Promise<{ refreshed: number }> {
  return request<{ refreshed: number }>(`${root(workspaceId)}/refresh`, {
    method: "POST",
  });
}

// ---------------------------------------------------------------------------
// Trades & dividends
// ---------------------------------------------------------------------------

export type TradeCreateInput = {
  accountId: string;
  instrumentId?: string;
  symbol?: string;
  name?: string;
  assetClass?: string;
  isin?: string | null;
  exchange?: string | null;
  side: "buy" | "sell";
  quantity: string;
  price: string;
  currency: string;
  feeAmount?: string;
  tradeDate: string;
  settleDate?: string | null;
};

export async function createTrade(
  workspaceId: string,
  body: TradeCreateInput
): Promise<Trade> {
  return request<Trade>(`${root(workspaceId)}/trades`, {
    method: "POST",
    json: body,
  });
}

export async function deleteTrade(
  workspaceId: string,
  tradeId: string
): Promise<void> {
  return request<void>(`${root(workspaceId)}/trades/${tradeId}`, {
    method: "DELETE",
  });
}

export async function fetchTrades(
  workspaceId: string,
  opts: { accountId?: string; instrumentId?: string } = {}
): Promise<Trade[]> {
  const qs: string[] = [];
  if (opts.accountId) qs.push(`accountId=${encodeURIComponent(opts.accountId)}`);
  if (opts.instrumentId)
    qs.push(`instrumentId=${encodeURIComponent(opts.instrumentId)}`);
  const suffix = qs.length ? `?${qs.join("&")}` : "";
  return request<Trade[]>(`${root(workspaceId)}/trades${suffix}`);
}

export type DividendCreateInput = {
  accountId: string;
  instrumentId?: string;
  symbol?: string;
  exDate?: string;
  payDate: string;
  amountPerUnit: string;
  currency: string;
  totalAmount: string;
  taxWithheld?: string;
};

export async function createDividend(
  workspaceId: string,
  body: DividendCreateInput
): Promise<DividendEvent> {
  return request<DividendEvent>(`${root(workspaceId)}/dividends`, {
    method: "POST",
    json: body,
  });
}

export async function deleteDividend(
  workspaceId: string,
  dividendId: string
): Promise<void> {
  return request<void>(`${root(workspaceId)}/dividends/${dividendId}`, {
    method: "DELETE",
  });
}

export { ApiError };
