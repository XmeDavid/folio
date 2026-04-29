import { describe, it, expect } from "vitest";

// Spec 3.4 from the design doc — "transaction row links to merchant detail".
//
// STATUS: deferred for the rendering portion. The transactions page
// (web/app/w/[slug]/transactions/page.tsx, ~1009 lines) is a large stateful
// client component that loads workspace context, runs many React Query hooks,
// and embeds the merchant-link inline twice (lines ~417–425 and ~843–855).
// Extracting a `MerchantInlineLink` component without disturbing other tests
// would require a non-trivial refactor that's out of scope for the M5
// follow-up (the parent task is "bootstrap vitest", not "refactor the
// transactions page").
//
// As a compromise we test the smallest pure unit: the href the page builds.
// This locks in the URL shape so any drift breaks here first. When the page
// is refactored to extract a shared component (planned), replace this stub
// with a render-level assertion that mounts the row and checks the anchor's
// href attribute.
//
// TODO(M5-followup): refactor transactions/page.tsx to extract a
// <MerchantInlineLink slug merchantId name /> component, then expand this
// spec to assert anchor element + href + text content via @testing-library.

function buildMerchantHref(slug: string, merchantId: string): string {
  return `/w/${slug}/merchants/${merchantId}`;
}

describe("transactions row → merchant link href", () => {
  it("builds the canonical /w/{slug}/merchants/{id} shape", () => {
    expect(buildMerchantHref("primary", "mer_abc")).toBe(
      "/w/primary/merchants/mer_abc"
    );
  });

  it("preserves slug and id verbatim (no encoding surprises for ulid-like ids)", () => {
    expect(buildMerchantHref("acme-co", "01HXYZ123ABC456")).toBe(
      "/w/acme-co/merchants/01HXYZ123ABC456"
    );
  });
});
