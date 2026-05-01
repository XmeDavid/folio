import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  render,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// Radix Dialog portals its content to document.body. Helpers below replace
// the previous `container.querySelector(...)` queries with `document.body`
// lookups so the migrated dialog stays testable.
const portalRoot = () => document.body;
const inPortal = <E extends Element>(selector: string) =>
  portalRoot().querySelector(selector) as E | null;
const allInPortal = <E extends Element>(selector: string) =>
  portalRoot().querySelectorAll(selector) as NodeListOf<E>;
import type * as ApiClient from "@/lib/api/client";
import type {
  Account,
  Transaction,
  TransactionWithTransfer,
} from "@/lib/api/client";

// Mocks must be declared before importing the SUT so vi.mock hoists correctly.
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
}));

vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof ApiClient>("@/lib/api/client");
  return {
    ...actual,
    fetchAccounts: vi.fn(),
    fetchTransactionsWithTransfers: vi.fn(),
    manualPairTransfer: vi.fn(),
  };
});

import {
  fetchAccounts,
  fetchTransactionsWithTransfers,
  manualPairTransfer,
} from "@/lib/api/client";
import { ManualPairDialog } from "@/components/transfers/manual-pair-dialog";

const mockedFetchAccounts = vi.mocked(fetchAccounts);
const mockedFetchCandidates = vi.mocked(fetchTransactionsWithTransfers);
const mockedManualPair = vi.mocked(manualPairTransfer);

const ACCOUNT_A: Account = {
  id: "acc_a",
  workspaceId: "ws_1",
  name: "Checking",
  kind: "depository" as Account["kind"],
  currency: "USD",
  accountSortOrder: 0,
  openDate: "2026-01-01T00:00:00Z",
  openingBalance: "0.00",
  openingBalanceDate: "2026-01-01T00:00:00Z",
  includeInNetworth: true,
  includeInSavingsRate: true,
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  balance: "0.00",
};

const ACCOUNT_B: Account = {
  ...ACCOUNT_A,
  id: "acc_b",
  name: "Savings",
};

const SOURCE_TX: Transaction = {
  id: "tx_source",
  workspaceId: "ws_1",
  accountId: ACCOUNT_A.id,
  status: "posted",
  bookedAt: "2026-04-20",
  amount: "-100.00",
  currency: "USD",
  counterpartyRaw: "Outgoing Wire",
  description: null,
  createdAt: "2026-04-20T00:00:00Z",
  updatedAt: "2026-04-20T00:00:00Z",
};

const CANDIDATE_TX: TransactionWithTransfer = {
  id: "tx_dest",
  workspaceId: "ws_1",
  accountId: ACCOUNT_B.id,
  status: "posted",
  bookedAt: "2026-04-21",
  amount: "100.00",
  currency: "USD",
  counterpartyRaw: "Incoming Wire",
  description: null,
  createdAt: "2026-04-21T00:00:00Z",
  updatedAt: "2026-04-21T00:00:00Z",
  transferMatchId: null,
  transferCounterpartId: null,
};

function renderDialog(
  opts: {
    open?: boolean;
  } = {}
) {
  const onClose = vi.fn();
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  mockedFetchAccounts.mockResolvedValue([ACCOUNT_A, ACCOUNT_B]);
  mockedFetchCandidates.mockResolvedValue([CANDIDATE_TX]);
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <ManualPairDialog
        open={opts.open ?? true}
        workspaceId="ws_1"
        source={SOURCE_TX}
        onClose={onClose}
      />
    </QueryClientProvider>
  );
  return { ...utils, onClose, queryClient };
}

beforeEach(() => {
  mockedFetchAccounts.mockReset();
  mockedFetchCandidates.mockReset();
  mockedManualPair.mockReset();
});

describe("<ManualPairDialog />", () => {
  it("renders no dialog when open=false", () => {
    renderDialog({ open: false });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(mockedFetchAccounts).not.toHaveBeenCalled();
    expect(mockedFetchCandidates).not.toHaveBeenCalled();
  });

  it("default mode shows the search input + a radio list of candidates", async () => {
    renderDialog();

    // Search input present.
    expect(inPortal<HTMLInputElement>("input#manual-pair-search")).not.toBeNull();

    // Wait for candidates to render as radios.
    await waitFor(() => {
      const radios = allInPortal<HTMLInputElement>(
        'input[type="radio"][name="manual-pair-target"]'
      );
      expect(radios.length).toBe(1);
    });

    // The candidate's counterparty appears in the list.
    expect(screen.getByRole("dialog").textContent).toContain("Incoming Wire");
  });

  it("toggling 'outbound to external' hides search/candidates and changes the Confirm label", async () => {
    renderDialog();

    // Wait for initial candidate list.
    await waitFor(() => {
      expect(
        allInPortal<HTMLInputElement>(
          'input[type="radio"][name="manual-pair-target"]'
        ).length
      ).toBe(1);
    });

    // Default Confirm label is "Pair selected".
    expect(screen.getByRole("dialog").textContent).toContain("Pair selected");

    // Toggle the external checkbox (the only checkbox in the dialog).
    const externalCheckbox = inPortal<HTMLInputElement>(
      'input[type="checkbox"]'
    );
    expect(externalCheckbox).not.toBeNull();
    fireEvent.click(externalCheckbox!);
    expect(externalCheckbox!.checked).toBe(true);

    // Search input + candidate radios are gone.
    expect(inPortal("input#manual-pair-search")).toBeNull();
    expect(
      allInPortal<HTMLInputElement>(
        'input[type="radio"][name="manual-pair-target"]'
      ).length
    ).toBe(0);

    // Confirm label flipped to "Mark as external".
    expect(screen.getByText("Mark as external")).not.toBeNull();
  });

  it("clicking Confirm in default mode with a chosen radio calls manualPairTransfer with the chosen destinationId", async () => {
    mockedManualPair.mockResolvedValue({
      id: "tm_1",
      workspaceId: "ws_1",
      sourceTransactionId: SOURCE_TX.id,
      destinationTransactionId: CANDIDATE_TX.id,
    } as unknown as Awaited<ReturnType<typeof manualPairTransfer>>);

    renderDialog();

    // Wait for candidate radio.
    let radio: HTMLInputElement | null = null;
    await waitFor(() => {
      radio = inPortal<HTMLInputElement>(
        'input[type="radio"][name="manual-pair-target"]'
      );
      expect(radio).not.toBeNull();
    });

    // Pick the candidate.
    fireEvent.click(radio!);
    expect(radio!.checked).toBe(true);

    // Confirm.
    fireEvent.click(screen.getByText("Pair selected"));

    await waitFor(() => {
      expect(mockedManualPair).toHaveBeenCalledWith("ws_1", {
        sourceId: SOURCE_TX.id,
        destinationId: CANDIDATE_TX.id,
      });
    });
  });

  it("clicking Confirm with the external toggle ON calls manualPairTransfer with destinationId=null", async () => {
    mockedManualPair.mockResolvedValue({
      id: "tm_2",
      workspaceId: "ws_1",
      sourceTransactionId: SOURCE_TX.id,
      destinationTransactionId: null,
    } as unknown as Awaited<ReturnType<typeof manualPairTransfer>>);

    renderDialog();

    // Wait for accounts query to settle so the dialog body is fully rendered.
    await waitFor(() => expect(mockedFetchAccounts).toHaveBeenCalled());

    // Toggle external on.
    const externalCheckbox = inPortal<HTMLInputElement>(
      'input[type="checkbox"]'
    );
    expect(externalCheckbox).not.toBeNull();
    fireEvent.click(externalCheckbox!);
    expect(externalCheckbox!.checked).toBe(true);

    // Confirm via the new label.
    fireEvent.click(screen.getByText("Mark as external"));

    await waitFor(() => {
      expect(mockedManualPair).toHaveBeenCalledWith("ws_1", {
        sourceId: SOURCE_TX.id,
        destinationId: null,
      });
    });
  });

  it("Cancel closes without calling manualPairTransfer", async () => {
    const { onClose } = renderDialog();
    await waitFor(() => expect(mockedFetchAccounts).toHaveBeenCalled());

    fireEvent.click(screen.getByText("Cancel"));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mockedManualPair).not.toHaveBeenCalled();
  });
});
