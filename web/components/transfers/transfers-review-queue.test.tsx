import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type * as ApiClient from "@/lib/api/client";
import type { Transaction, TransferCandidate } from "@/lib/api/client";

// Mocks must be declared before importing the SUT so vi.mock hoists correctly.
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
}));

vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof ApiClient>("@/lib/api/client");
  return {
    ...actual,
    fetchPendingTransferCandidates: vi.fn(),
    fetchTransaction: vi.fn(),
    manualPairTransfer: vi.fn(),
    declineTransferCandidate: vi.fn(),
  };
});

import {
  fetchPendingTransferCandidates,
  fetchTransaction,
  manualPairTransfer,
  declineTransferCandidate,
} from "@/lib/api/client";
import { TransfersReviewQueue } from "@/components/transfers/transfers-review-queue";

const mockedFetchCandidates = vi.mocked(fetchPendingTransferCandidates);
const mockedFetchTransaction = vi.mocked(fetchTransaction);
const mockedManualPair = vi.mocked(manualPairTransfer);
const mockedDecline = vi.mocked(declineTransferCandidate);

const SOURCE_TX: Transaction = {
  id: "tx_source",
  workspaceId: "ws_1",
  accountId: "acc_a",
  status: "posted",
  bookedAt: "2026-04-20",
  amount: "-100.00",
  currency: "USD",
  counterpartyRaw: "Outgoing Wire",
  description: null,
  createdAt: "2026-04-20T00:00:00Z",
  updatedAt: "2026-04-20T00:00:00Z",
};

const DEST_TX: Transaction = {
  id: "tx_dest",
  workspaceId: "ws_1",
  accountId: "acc_b",
  status: "posted",
  bookedAt: "2026-04-21",
  amount: "100.00",
  currency: "USD",
  counterpartyRaw: "Incoming Wire",
  description: null,
  createdAt: "2026-04-21T00:00:00Z",
  updatedAt: "2026-04-21T00:00:00Z",
};

const CANDIDATE: TransferCandidate = {
  id: "cand_1",
  workspaceId: "ws_1",
  sourceTransactionId: SOURCE_TX.id,
  candidateDestinationIds: [DEST_TX.id],
  status: "pending",
  suggestedAt: "2026-04-22T00:00:00Z",
};

function renderQueue() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <TransfersReviewQueue workspaceId="ws_1" />
    </QueryClientProvider>
  );
  return { ...utils, queryClient };
}

beforeEach(() => {
  mockedFetchCandidates.mockReset();
  mockedFetchTransaction.mockReset();
  mockedManualPair.mockReset();
  mockedDecline.mockReset();
});

describe("<TransfersReviewQueue />", () => {
  it("shows the empty state when there are no pending candidates", async () => {
    mockedFetchCandidates.mockResolvedValue([]);
    const { container } = renderQueue();
    await waitFor(() => {
      expect(container.textContent).toContain("No pending suggestions");
    });
    expect(mockedFetchTransaction).not.toHaveBeenCalled();
  });

  it("renders source line, destination radios, and action buttons", async () => {
    mockedFetchCandidates.mockResolvedValue([CANDIDATE]);
    mockedFetchTransaction.mockImplementation(async (_ws, txId) => {
      if (txId === SOURCE_TX.id) return SOURCE_TX;
      if (txId === DEST_TX.id) return DEST_TX;
      throw new Error(`unexpected tx id ${txId}`);
    });

    const { container, getByRole } = renderQueue();

    // Wait for source line + destination row to render.
    await waitFor(() => {
      expect(container.textContent).toContain("Outgoing Wire");
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Incoming Wire");
    });

    // Destination radio is present.
    const radio = container.querySelector(
      'input[type="radio"][name="cand-cand_1"]'
    ) as HTMLInputElement | null;
    expect(radio).not.toBeNull();
    expect(radio!.value).toBe(DEST_TX.id);

    // Both action buttons render.
    expect(
      getByRole("button", { name: /pair selected/i })
    ).not.toBeNull();
    expect(
      getByRole("button", { name: /external credit/i })
    ).not.toBeNull();
  });

  it("clicking 'Pair selected' with a chosen destination calls manualPairTransfer", async () => {
    mockedFetchCandidates.mockResolvedValue([CANDIDATE]);
    mockedFetchTransaction.mockImplementation(async (_ws, txId) => {
      if (txId === SOURCE_TX.id) return SOURCE_TX;
      if (txId === DEST_TX.id) return DEST_TX;
      throw new Error(`unexpected tx id ${txId}`);
    });
    mockedManualPair.mockResolvedValue({
      id: "tm_1",
      workspaceId: "ws_1",
      sourceTransactionId: SOURCE_TX.id,
      destinationTransactionId: DEST_TX.id,
    } as unknown as Awaited<ReturnType<typeof manualPairTransfer>>);

    const { container, getByRole } = renderQueue();

    await waitFor(() => {
      expect(container.textContent).toContain("Incoming Wire");
    });

    // Choose the destination.
    const radio = container.querySelector(
      'input[type="radio"][name="cand-cand_1"]'
    ) as HTMLInputElement | null;
    expect(radio).not.toBeNull();
    fireEvent.click(radio!);

    fireEvent.click(getByRole("button", { name: /pair selected/i }));

    await waitFor(() => {
      expect(mockedManualPair).toHaveBeenCalledWith("ws_1", {
        sourceId: SOURCE_TX.id,
        destinationId: DEST_TX.id,
      });
    });
  });

  it("clicking 'External credit' calls declineTransferCandidate(workspaceId, candidateId)", async () => {
    mockedFetchCandidates.mockResolvedValue([CANDIDATE]);
    mockedFetchTransaction.mockImplementation(async (_ws, txId) => {
      if (txId === SOURCE_TX.id) return SOURCE_TX;
      if (txId === DEST_TX.id) return DEST_TX;
      throw new Error(`unexpected tx id ${txId}`);
    });
    mockedDecline.mockResolvedValue(undefined);

    const { container, getByRole } = renderQueue();

    await waitFor(() => {
      expect(container.textContent).toContain("Outgoing Wire");
    });

    fireEvent.click(getByRole("button", { name: /external credit/i }));

    await waitFor(() => {
      expect(mockedDecline).toHaveBeenCalledWith("ws_1", CANDIDATE.id);
    });
    expect(mockedManualPair).not.toHaveBeenCalled();
  });
});
