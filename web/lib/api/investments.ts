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
  dailyChange?: string;
  dailyChangePct?: string;
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
  topProfits: HoldingMover[];
  topLosses: HoldingMover[];
  warnings?: string[];
};

export type PortfolioHistoryPoint = {
  date: string;
  value: string;
  reportCurrency: string;
};

export type HistoryDataPoint = {
  date: string;
  quantity: string;
  price?: string | null;
  value?: string | null;
  valueNative?: string | null;
  currency: string;
  nativeCurrency: string;
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
  reportCurrency: string;
  positions: Position[];
  trades: Trade[];
  dividends: DividendEvent[];
  history: HistoryDataPoint[];
  lastQuote?: QuoteSnapshot | null;
};

// ---------------------------------------------------------------------------
// API
// ---------------------------------------------------------------------------

const root = (workspaceId: string) => `/api/v1/t/${workspaceId}/investments`;

export async function fetchDashboard(
  workspaceId: string,
  opts: { accountId?: string; currency?: string } = {}
): Promise<DashboardSummary> {
  const qs: string[] = [];
  if (opts.accountId)
    qs.push(`accountId=${encodeURIComponent(opts.accountId)}`);
  if (opts.currency) qs.push(`currency=${encodeURIComponent(opts.currency)}`);
  const suffix = qs.length ? `?${qs.join("&")}` : "";
  return request<DashboardSummary>(`${root(workspaceId)}/dashboard${suffix}`);
}

export async function fetchDashboardHistory(
  workspaceId: string,
  opts: { accountId?: string; currency?: string; range?: string } = {}
): Promise<PortfolioHistoryPoint[]> {
  const qs: string[] = [];
  if (opts.accountId)
    qs.push(`accountId=${encodeURIComponent(opts.accountId)}`);
  if (opts.currency) qs.push(`currency=${encodeURIComponent(opts.currency)}`);
  if (opts.range) qs.push(`range=${encodeURIComponent(opts.range)}`);
  const suffix = qs.length ? `?${qs.join("&")}` : "";
  return request<PortfolioHistoryPoint[]>(
    `${root(workspaceId)}/dashboard/history${suffix}`
  );
}

export async function fetchPositions(
  workspaceId: string,
  opts: { accountId?: string; status?: "open" | "closed"; search?: string } = {}
): Promise<Position[]> {
  const qs: string[] = [];
  if (opts.accountId)
    qs.push(`accountId=${encodeURIComponent(opts.accountId)}`);
  if (opts.status) qs.push(`status=${opts.status}`);
  if (opts.search) qs.push(`search=${encodeURIComponent(opts.search)}`);
  const suffix = qs.length ? `?${qs.join("&")}` : "";
  return request<Position[]>(`${root(workspaceId)}/positions${suffix}`);
}

export async function fetchInstrumentDetail(
  workspaceId: string,
  instrumentIdOrSymbol: string,
  opts: { currency?: string } = {}
): Promise<InstrumentDetail> {
  const suffix = opts.currency
    ? `?currency=${encodeURIComponent(opts.currency)}`
    : "";
  return request<InstrumentDetail>(
    `${root(workspaceId)}/instruments/${encodeURIComponent(instrumentIdOrSymbol)}${suffix}`
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
  if (opts.accountId)
    qs.push(`accountId=${encodeURIComponent(opts.accountId)}`);
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

export async function fetchDividends(
  workspaceId: string,
  opts: { accountId?: string; instrumentId?: string } = {}
): Promise<DividendEvent[]> {
  const qs: string[] = [];
  if (opts.accountId)
    qs.push(`accountId=${encodeURIComponent(opts.accountId)}`);
  if (opts.instrumentId)
    qs.push(`instrumentId=${encodeURIComponent(opts.instrumentId)}`);
  const suffix = qs.length ? `?${qs.join("&")}` : "";
  return request<DividendEvent[]>(`${root(workspaceId)}/dividends${suffix}`);
}

// ---------------------------------------------------------------------------
// Corporate actions (manual entry: splits, delistings, etc.)
// ---------------------------------------------------------------------------

export type CorporateActionKind =
  | "split"
  | "reverse_split"
  | "merger"
  | "spinoff"
  | "delisting"
  | "symbol_change"
  | "cash_distribution"
  | "stock_distribution";

export type CorporateAction = {
  id: string;
  workspaceId?: string | null;
  accountId?: string | null;
  instrumentId: string;
  symbol: string;
  kind: CorporateActionKind;
  effectiveDate: string;
  payload: Record<string, unknown>;
  appliedAt?: string | null;
  createdAt: string;
};

export type CorporateActionCreateInput = {
  accountId?: string | null;
  instrumentId?: string;
  symbol?: string;
  kind: CorporateActionKind;
  effectiveDate: string;
  factor?: string;
  amount?: string;
  newSymbol?: string;
};

export async function fetchCorporateActions(
  workspaceId: string,
  instrumentId: string
): Promise<CorporateAction[]> {
  return request<CorporateAction[]>(
    `${root(workspaceId)}/corporate-actions?instrumentId=${encodeURIComponent(instrumentId)}`
  );
}

export async function createCorporateAction(
  workspaceId: string,
  body: CorporateActionCreateInput
): Promise<CorporateAction> {
  return request<CorporateAction>(`${root(workspaceId)}/corporate-actions`, {
    method: "POST",
    json: body,
  });
}

export async function deleteCorporateAction(
  workspaceId: string,
  actionId: string
): Promise<void> {
  return request<void>(`${root(workspaceId)}/corporate-actions/${actionId}`, {
    method: "DELETE",
  });
}

// ---------------------------------------------------------------------------
// Imports
// ---------------------------------------------------------------------------

export type ImportFormat = "ibkr" | "revolut_trading";

export type ImportSummary = {
  tradesCreated: number;
  dividendsCreated: number;
  instrumentsTouched: number;
  skipped: number;
  warnings?: string[];
};

export async function uploadInvestmentImport(
  workspaceId: string,
  _format: ImportFormat,
  _accountId: string,
  file: File
): Promise<ImportSummary> {
  const form = new FormData();
  form.append("file", file);
  const res = await fetch(
    `${baseUrl}/api/v1/t/${workspaceId}/accounts/import-preview`,
    {
      method: "POST",
      credentials: "include",
      headers: { [CSRF_HEADER_NAME]: CSRF_HEADER_VALUE },
      body: form,
    }
  );
  if (!res.ok) {
    let parsed: unknown;
    try {
      parsed = await res.json();
    } catch {
      parsed = undefined;
    }
    throw new ApiError(res.status, parsed);
  }
  const body = (await res.json()) as {
    kind?: string;
    investment?: { summary?: ImportSummary };
  };
  if (body.kind !== "investment" || !body.investment?.summary) {
    throw new ApiError(400, {
      error: "file was not recognised as an investment import",
    });
  }
  return body.investment.summary;
}

export { ApiError };
