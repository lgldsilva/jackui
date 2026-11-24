import type { SystemStatus } from '../api/client'

/** Poll cadence for GET /status while the app is open. */
export const DEGRADED_POLL_MS = 30_000

/**
 * True only for an edge node that reports its database as a read-only standby.
 * A failed /status fetch (null) is NOT read-only mode — that is the node itself
 * being down, which other error states already surface.
 */
export function isReadOnlyDegraded(status: Pick<SystemStatus, 'degraded' | 'role'> | null | undefined): boolean {
  return status?.role === 'edge' && status.degraded === true
}
