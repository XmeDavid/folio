import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  createMerchant,
  updateMerchant,
  archiveMerchant,
  fetchMerchant,
  listMerchantAliases,
  addMerchantAlias,
  removeMerchantAlias,
  previewMergeMerchants,
  mergeMerchants,
  type Merchant,
  type MerchantAlias,
  type MergePreview,
  type MergeResult,
  type MerchantPatchResult,
} from "@/lib/api/client";

// Tests for the merchants slice of the API client. The goal is to cover one
// invocation per HTTP verb (GET, POST, PATCH, DELETE) and confirm the
// URL/method/body shape plus that the parsed response is returned typed.

const WS = "ws_123";
const M_ID = "mer_abc";
const ALIAS_ID = "ma_xyz";

type FetchCall = {
  url: string;
  init: RequestInit;
};

function captureFetch<T>(response: T, status = 200): { fetchMock: ReturnType<typeof vi.fn>; calls: FetchCall[] } {
  const calls: FetchCall[] = [];
  const fetchMock = vi.fn((input: RequestInfo | URL, init: RequestInit = {}) => {
    calls.push({ url: String(input), init });
    return Promise.resolve(
      new Response(status === 204 ? null : JSON.stringify(response), {
        status,
        headers: status === 204 ? undefined : { "Content-Type": "application/json" },
      })
    );
  });
  // Reassign global fetch — the client uses `fetch(...)` directly with no DI.
  vi.stubGlobal("fetch", fetchMock);
  return { fetchMock, calls };
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("merchants api client", () => {
  it("createMerchant POSTs JSON to /merchants and returns the parsed Merchant", async () => {
    const merchant: Merchant = {
      id: M_ID,
      workspaceId: WS,
      canonicalName: "Spotify",
      defaultCategoryId: null,
      industry: null,
      website: null,
      notes: null,
      logoUrl: null,
      archivedAt: null,
      createdAt: "2026-04-29T00:00:00Z",
      updatedAt: "2026-04-29T00:00:00Z",
    };
    const { calls } = captureFetch(merchant);

    const result = await createMerchant(WS, { canonicalName: "Spotify" });

    expect(calls).toHaveLength(1);
    const call = calls[0]!;
    expect(call.url).toBe(`/api/v1/t/${WS}/merchants`);
    expect(call.init.method).toBe("POST");
    expect((call.init.headers as Record<string, string>)["Content-Type"]).toBe(
      "application/json"
    );
    expect((call.init.headers as Record<string, string>)["X-Folio-Request"]).toBe("1");
    expect(call.init.credentials).toBe("include");
    expect(JSON.parse(String(call.init.body))).toEqual({ canonicalName: "Spotify" });
    expect(result).toEqual(merchant);
  });

  it("fetchMerchant GETs /merchants/:id", async () => {
    const merchant: Merchant = {
      id: M_ID,
      workspaceId: WS,
      canonicalName: "Spotify",
      defaultCategoryId: null,
      industry: null,
      website: null,
      notes: null,
      logoUrl: null,
      archivedAt: null,
      createdAt: "2026-04-29T00:00:00Z",
      updatedAt: "2026-04-29T00:00:00Z",
    };
    const { calls } = captureFetch(merchant);

    const result = await fetchMerchant(WS, M_ID);

    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(`/api/v1/t/${WS}/merchants/${M_ID}`);
    expect(calls[0]!.init.method).toBe("GET");
    // GET should not send a body.
    expect(calls[0]!.init.body).toBeUndefined();
    expect(result).toEqual(merchant);
  });

  it("updateMerchant PATCHes with cascade in the body and returns MerchantPatchResult", async () => {
    const merchant: Merchant = {
      id: M_ID,
      workspaceId: WS,
      canonicalName: "Spotify",
      defaultCategoryId: "cat_new",
      industry: null,
      website: null,
      notes: null,
      logoUrl: null,
      archivedAt: null,
      createdAt: "2026-04-29T00:00:00Z",
      updatedAt: "2026-04-29T00:00:00Z",
    };
    const response: MerchantPatchResult = {
      merchant,
      cascadedTransactionCount: 17,
    };
    const { calls } = captureFetch(response);

    const result = await updateMerchant(WS, M_ID, {
      defaultCategoryId: "cat_new",
      cascade: true,
    });

    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(`/api/v1/t/${WS}/merchants/${M_ID}`);
    expect(calls[0]!.init.method).toBe("PATCH");
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({
      defaultCategoryId: "cat_new",
      cascade: true,
    });
    expect(result).toEqual(response);
    expect(result.cascadedTransactionCount).toBe(17);
  });

  it("archiveMerchant DELETEs /merchants/:id and resolves to undefined on 204", async () => {
    const { calls } = captureFetch(null, 204);

    const result = await archiveMerchant(WS, M_ID);

    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(`/api/v1/t/${WS}/merchants/${M_ID}`);
    expect(calls[0]!.init.method).toBe("DELETE");
    expect(result).toBeUndefined();
  });

  it("listMerchantAliases GETs /merchants/:id/aliases", async () => {
    const aliases: MerchantAlias[] = [
      {
        id: ALIAS_ID,
        workspaceId: WS,
        merchantId: M_ID,
        rawPattern: "SPOTIFY*USA",
        createdAt: "2026-04-29T00:00:00Z",
      },
    ];
    const { calls } = captureFetch(aliases);

    const result = await listMerchantAliases(WS, M_ID);

    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(`/api/v1/t/${WS}/merchants/${M_ID}/aliases`);
    expect(calls[0]!.init.method).toBe("GET");
    expect(result).toEqual(aliases);
  });

  it("addMerchantAlias POSTs the rawPattern body", async () => {
    const alias: MerchantAlias = {
      id: ALIAS_ID,
      workspaceId: WS,
      merchantId: M_ID,
      rawPattern: "SPOTIFY*USA",
      createdAt: "2026-04-29T00:00:00Z",
    };
    const { calls } = captureFetch(alias);

    const result = await addMerchantAlias(WS, M_ID, { rawPattern: "SPOTIFY*USA" });

    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(`/api/v1/t/${WS}/merchants/${M_ID}/aliases`);
    expect(calls[0]!.init.method).toBe("POST");
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ rawPattern: "SPOTIFY*USA" });
    expect(result).toEqual(alias);
  });

  it("removeMerchantAlias DELETEs /merchants/:id/aliases/:aliasId", async () => {
    const { calls } = captureFetch(null, 204);

    const result = await removeMerchantAlias(WS, M_ID, ALIAS_ID);

    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(
      `/api/v1/t/${WS}/merchants/${M_ID}/aliases/${ALIAS_ID}`
    );
    expect(calls[0]!.init.method).toBe("DELETE");
    expect(result).toBeUndefined();
  });

  it("previewMergeMerchants POSTs targetId to /merge/preview", async () => {
    const preview: MergePreview = {
      sourceCanonicalName: "spotify usa",
      targetCanonicalName: "Spotify",
      movedCount: 42,
      capturedAliasCount: 3,
      cascadedCountIfApplied: 12,
      blankFillFields: ["industry", "website"],
    };
    const { calls } = captureFetch(preview);

    const result = await previewMergeMerchants(WS, M_ID, { targetId: "mer_target" });

    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(
      `/api/v1/t/${WS}/merchants/${M_ID}/merge/preview`
    );
    expect(calls[0]!.init.method).toBe("POST");
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ targetId: "mer_target" });
    expect(result).toEqual(preview);
    expect(result.cascadedCountIfApplied).toBe(12);
  });

  it("mergeMerchants POSTs targetId + applyTargetDefault to /merge", async () => {
    const merged: MergeResult = {
      target: {
        id: "mer_target",
        workspaceId: WS,
        canonicalName: "Spotify",
        defaultCategoryId: null,
        industry: null,
        website: null,
        notes: null,
        logoUrl: null,
        archivedAt: null,
        createdAt: "2026-04-29T00:00:00Z",
        updatedAt: "2026-04-29T00:00:00Z",
      },
      movedCount: 42,
      cascadedCount: 12,
      capturedAliasCount: 3,
    };
    const { calls } = captureFetch(merged);

    const result = await mergeMerchants(WS, M_ID, {
      targetId: "mer_target",
      applyTargetDefault: true,
    });

    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(`/api/v1/t/${WS}/merchants/${M_ID}/merge`);
    expect(calls[0]!.init.method).toBe("POST");
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({
      targetId: "mer_target",
      applyTargetDefault: true,
    });
    expect(result).toEqual(merged);
  });

  it("propagates non-2xx responses as ApiError (smoke check via createMerchant)", async () => {
    captureFetch({ error: "nope" }, 422);
    await expect(createMerchant(WS, { canonicalName: "" })).rejects.toMatchObject({
      status: 422,
    });
  });
});
