// Typed OpenAPI client. Schema is generated from openapi/openapi.yaml via
// `pnpm openapi:gen` — see package.json.
//
// After generating the schema, replace the `any` below with `paths` from schema.
//
//   import type { paths } from "./schema";
//   const client = createClient<paths>({ baseUrl });

import createClient from "openapi-fetch";

const baseUrl =
  typeof window === "undefined"
    ? (process.env.API_URL ?? "http://localhost:8080")
    : ""; // browser uses Next rewrite

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const api = createClient<any>({ baseUrl });
