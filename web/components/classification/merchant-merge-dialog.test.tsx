import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  render,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type * as ApiClient from "@/lib/api/client";
import type { Merchant, MergePreview } from "@/lib/api/client";

// Radix Dialog portals its content to document.body, so element lookups go
// through `screen` / document.body rather than the container returned by
// render().
const inPortal = <E extends Element>(selector: string) =>
  document.body.querySelector(selector) as E | null;

// Mocks must be declared before importing the SUT so vi.mock hoists correctly.
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
}));

vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof ApiClient>("@/lib/api/client");
  return {
    ...actual,
    fetchMerchants: vi.fn(),
    previewMergeMerchants: vi.fn(),
    mergeMerchants: vi.fn(),
  };
});

import {
  fetchMerchants,
  previewMergeMerchants,
  mergeMerchants,
} from "@/lib/api/client";
import { MerchantMergeDialog } from "@/components/classification/merchant-merge-dialog";

const mockedFetchMerchants = vi.mocked(fetchMerchants);
const mockedPreview = vi.mocked(previewMergeMerchants);
const mockedMerge = vi.mocked(mergeMerchants);

const SOURCE: Merchant = {
  id: "mer_source",
  workspaceId: "ws_1",
  canonicalName: "spotify usa",
  defaultCategoryId: null,
  industry: null,
  website: null,
  notes: null,
  logoUrl: null,
  archivedAt: null,
  createdAt: "2026-04-29T00:00:00Z",
  updatedAt: "2026-04-29T00:00:00Z",
};

const TARGET: Merchant = {
  id: "mer_target",
  workspaceId: "ws_1",
  canonicalName: "Spotify",
  defaultCategoryId: "cat_music",
  industry: "Streaming",
  website: null,
  notes: null,
  logoUrl: null,
  archivedAt: null,
  createdAt: "2026-04-29T00:00:00Z",
  updatedAt: "2026-04-29T00:00:00Z",
};

function renderDialog(
  opts: { open?: boolean; preview?: MergePreview } = {}
) {
  const onClose = vi.fn();
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  mockedFetchMerchants.mockResolvedValue([SOURCE, TARGET]);
  if (opts.preview) {
    mockedPreview.mockResolvedValue(opts.preview);
  }
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <MerchantMergeDialog
        open={opts.open ?? true}
        workspaceId="ws_1"
        workspaceSlug="primary"
        source={SOURCE}
        onClose={onClose}
      />
    </QueryClientProvider>
  );
  return { ...utils, onClose, queryClient };
}

const dialogText = () => screen.getByRole("dialog").textContent ?? "";

beforeEach(() => {
  mockedFetchMerchants.mockReset();
  mockedPreview.mockReset();
  mockedMerge.mockReset();
});

describe("<MerchantMergeDialog />", () => {
  it("renders no dialog when open=false", () => {
    renderDialog({ open: false });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(mockedFetchMerchants).not.toHaveBeenCalled();
  });

  it("renders the search input when open=true", async () => {
    renderDialog();
    expect(
      inPortal<HTMLInputElement>("input#merchant-merge-search")
    ).not.toBeNull();
    // Wait for fetchMerchants to settle so the query state is consistent.
    await waitFor(() => expect(mockedFetchMerchants).toHaveBeenCalled());
  });

  it("selecting a target fires the preview query and renders preview counts", async () => {
    const preview: MergePreview = {
      sourceCanonicalName: "spotify usa",
      targetCanonicalName: "Spotify",
      movedCount: 42,
      capturedAliasCount: 3,
      cascadedCountIfApplied: 0,
      blankFillFields: [],
    };
    renderDialog({ preview });

    // Wait for the candidate list to render.
    await waitFor(() => {
      expect(dialogText()).toContain("Spotify");
    });

    // Click the candidate row to select target.
    fireEvent.click(screen.getByText("Spotify"));

    await waitFor(() => {
      expect(mockedPreview).toHaveBeenCalledWith("ws_1", SOURCE.id, {
        targetId: TARGET.id,
      });
    });

    // Preview counts render.
    await waitFor(() => {
      expect(dialogText()).toContain("42");
      expect(dialogText()).toContain("3");
    });
  });

  it("hides the 'apply target default' checkbox when cascadedCountIfApplied=0", async () => {
    const preview: MergePreview = {
      sourceCanonicalName: "spotify usa",
      targetCanonicalName: "Spotify",
      movedCount: 42,
      capturedAliasCount: 3,
      cascadedCountIfApplied: 0,
      blankFillFields: [],
    };
    renderDialog({ preview });
    await waitFor(() => expect(dialogText()).toContain("Spotify"));
    fireEvent.click(screen.getByText("Spotify"));
    await waitFor(() => expect(mockedPreview).toHaveBeenCalled());

    // Wait for preview render then assert no checkbox exists.
    await waitFor(() => expect(dialogText()).toContain("Move"));
    expect(inPortal('input[type="checkbox"]')).toBeNull();
  });

  it("shows the checkbox when cascadedCountIfApplied > 0 and merge POSTs applyTargetDefault=true when checked", async () => {
    const preview: MergePreview = {
      sourceCanonicalName: "spotify usa",
      targetCanonicalName: "Spotify",
      movedCount: 42,
      capturedAliasCount: 3,
      cascadedCountIfApplied: 12,
      blankFillFields: [],
    };
    mockedMerge.mockResolvedValue({
      target: TARGET,
      movedCount: 42,
      cascadedCount: 12,
      capturedAliasCount: 3,
    });

    renderDialog({ preview });
    await waitFor(() => expect(dialogText()).toContain("Spotify"));
    fireEvent.click(screen.getByText("Spotify"));
    await waitFor(() => expect(mockedPreview).toHaveBeenCalled());

    // Checkbox visible (defaults checked).
    let checkbox: HTMLInputElement | null = null;
    await waitFor(() => {
      checkbox = inPortal<HTMLInputElement>('input[type="checkbox"]');
      expect(checkbox).not.toBeNull();
    });
    expect(checkbox!.checked).toBe(true);

    // Click confirm.
    fireEvent.click(screen.getByText("Confirm merge"));

    await waitFor(() => {
      expect(mockedMerge).toHaveBeenCalledWith("ws_1", SOURCE.id, {
        targetId: TARGET.id,
        applyTargetDefault: true,
      });
    });
  });

  it("merge sends applyTargetDefault=false when the checkbox is unchecked", async () => {
    const preview: MergePreview = {
      sourceCanonicalName: "spotify usa",
      targetCanonicalName: "Spotify",
      movedCount: 5,
      capturedAliasCount: 1,
      cascadedCountIfApplied: 3,
      blankFillFields: [],
    };
    mockedMerge.mockResolvedValue({
      target: TARGET,
      movedCount: 5,
      cascadedCount: 0,
      capturedAliasCount: 1,
    });
    renderDialog({ preview });
    await waitFor(() => expect(dialogText()).toContain("Spotify"));
    fireEvent.click(screen.getByText("Spotify"));
    await waitFor(() => expect(mockedPreview).toHaveBeenCalled());

    // Untick the cascade checkbox.
    let checkbox: HTMLInputElement | null = null;
    await waitFor(() => {
      checkbox = inPortal<HTMLInputElement>('input[type="checkbox"]');
      expect(checkbox).not.toBeNull();
    });
    fireEvent.click(checkbox!);
    expect(checkbox!.checked).toBe(false);

    fireEvent.click(screen.getByText("Confirm merge"));

    await waitFor(() => {
      expect(mockedMerge).toHaveBeenCalledWith("ws_1", SOURCE.id, {
        targetId: TARGET.id,
        applyTargetDefault: false,
      });
    });
  });

  it("clicking Cancel does not call mergeMerchants", async () => {
    const { onClose } = renderDialog();
    await waitFor(() => expect(mockedFetchMerchants).toHaveBeenCalled());
    fireEvent.click(screen.getByText("Cancel"));
    expect(mockedMerge).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
