// Shared transport for the typed Folio API client and feature-specific
// helpers (investments, etc). Centralises CSRF handling, error parsing,
// and the JSON / multipart fetch wrappers so all callers see the same
// `ApiError` identity (`instanceof ApiError` works regardless of which
// helper threw it).

const CSRF_HEADER_NAME = "X-Folio-Request";
const CSRF_HEADER_VALUE = "1";

export const baseUrl =
  typeof window === "undefined"
    ? (process.env.API_URL ?? "http://localhost:8080")
    : ""; // browser uses Next rewrite

export type ErrorBody = {
  error?: string;
  code?: string;
  details?: unknown;
};

export class ApiError extends Error {
  status: number;
  body: ErrorBody | undefined;
  constructor(status: number, body: unknown) {
    const b = (body as ErrorBody) ?? undefined;
    super(b?.error || `Request failed (${status})`);
    this.status = status;
    this.body = b;
  }
}

export function defaultHeaders(extra?: HeadersInit): HeadersInit {
  const base: Record<string, string> = {
    [CSRF_HEADER_NAME]: CSRF_HEADER_VALUE,
  };
  if (!extra) return base;
  return { ...base, ...(extra as Record<string, string>) };
}

export async function parseError(res: Response): Promise<ApiError> {
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    body = undefined;
  }
  return new ApiError(res.status, body);
}

export async function request<T>(
  path: string,
  init: RequestInit & { json?: unknown } = {}
): Promise<T> {
  const { json, headers, ...rest } = init;
  const mergedHeaders: Record<string, string> = {
    ...(defaultHeaders(headers) as Record<string, string>),
  };
  let body = rest.body;
  if (json !== undefined) {
    mergedHeaders["Content-Type"] = "application/json";
    body = JSON.stringify(json);
  }
  const res = await fetch(`${baseUrl}${path}`, {
    ...rest,
    credentials: "include",
    headers: mergedHeaders,
    body,
  });
  if (!res.ok) {
    throw await parseError(res);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export async function uploadRequest<T>(
  path: string,
  form: FormData
): Promise<T> {
  const res = await fetch(`${baseUrl}${path}`, {
    method: "POST",
    credentials: "include",
    headers: defaultHeaders(),
    body: form,
  });
  if (!res.ok) {
    throw await parseError(res);
  }
  return (await res.json()) as T;
}
