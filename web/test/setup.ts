// Vitest setup. Kept intentionally minimal — we don't pull in
// @testing-library/jest-dom to avoid an extra dev-dependency and instead lean
// on plain DOM assertions in the specs (e.g. `expect(el).not.toBeNull()`,
// `expect(el.textContent).toContain(...)`). If matchers like `toBeInTheDocument`
// become useful later, install `@testing-library/jest-dom` and import it here:
//
//   import "@testing-library/jest-dom/vitest";

import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

// Tear down React Testing Library renders between tests so DOM state from the
// previous test never leaks into the next.
afterEach(() => {
  cleanup();
});
