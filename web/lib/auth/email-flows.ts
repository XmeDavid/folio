"use client";

async function post(path: string, body: unknown = {}) {
  const res = await fetch(path, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error((data as { error?: string }).error ?? `Request failed (${res.status})`);
  }
}

export function verifyEmail(token: string) {
  return post("/api/v1/auth/verify", { token });
}

export function requestPasswordReset(email: string) {
  return post("/api/v1/auth/password/reset-request", { email });
}

export function confirmPasswordReset(token: string, newPassword: string) {
  return post("/api/v1/auth/password/reset-confirm", { token, newPassword });
}

export function resendVerification() {
  return post("/api/v1/auth/verify/resend");
}

export function requestEmailChange(email: string) {
  return post("/api/v1/auth/email/change-request", { email });
}

export function confirmEmailChange(token: string) {
  return post("/api/v1/auth/email/change-confirm", { token });
}
