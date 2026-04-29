/**
 * Friendly, user-facing messages for typed API error codes returned by the
 * Folio backend. For unknown codes the caller's fallback is used unchanged,
 * so this map is purely additive.
 */
export const FRIENDLY_ERROR: Record<string, string> = {
  last_owner:
    "You're the only owner — promote someone else first.",
  last_workspace:
    "You can't leave your only workspace. Create another one first.",
  email_mismatch:
    "This invite was sent to a different email.",
  email_unverified:
    "Verify your email address before accepting this invite.",
  invite_expired:
    "This invite has expired. Ask the inviter to send a new one.",
  invite_revoked:
    "This invite was revoked.",
  invite_already_used:
    "This invite has already been used.",
  invite_not_found:
    "We couldn't find that invite.",
  not_a_member:
    "You don't have access to that workspace.",
  reauth_required:
    "This action requires recent sign-in. Sign out and back in to continue.",
  forbidden:
    "You don't have permission to do that.",
};

/**
 * Returns a friendly message for a known typed error code, or `fallback` for
 * anything not in the map.
 */
export function friendlyError(
  code: string | undefined,
  fallback: string
): string {
  return (code && FRIENDLY_ERROR[code]) ?? fallback;
}
