/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQuery } from '@tanstack/react-query'

import { getAuditLogs } from '@/features/usage-logs/audit/api'
import {
  hasPermission,
  ADMIN_PERMISSION_RESOURCES,
  ADMIN_PERMISSION_ACTIONS,
} from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { useNotificationStore } from '@/stores/notification-store'

const EMPTY_IDS: string[] = []

// Recent alerts share one query across the sidebar and the audit view. Only
// opaque read IDs are persisted, separately for each signed-in administrator.
export function useAdminNotifications() {
  const user = useAuthStore((state) => state.auth.user)
  const enabled =
    !!user &&
    user.role >= ROLE.ADMIN &&
    hasPermission(
      user,
      ADMIN_PERMISSION_RESOURCES.AUDIT,
      ADMIN_PERMISSION_ACTIONS.READ
    )
  const readIds = useNotificationStore((state) =>
    user ? (state.adminReadIds[user.id] ?? EMPTY_IDS) : EMPTY_IDS
  )
  const markRead = useNotificationStore((state) => state.markAdminAlertsRead)
  const query = useQuery({
    queryKey: ['admin-notifications', user?.id, user?.role],
    queryFn: () =>
      getAuditLogs('all', { p: 1, page_size: 100, category: 'system' }),
    enabled,
    staleTime: 15_000,
    refetchInterval: 30_000,
    retry: false,
    meta: { errorToast: false },
  })
  const items = enabled && !query.isError ? (query.data?.items ?? []) : []
  const unreadCount = items.filter(
    (entry) => !readIds.includes(entry.event_id)
  ).length
  return {
    enabled,
    unreadCount,
    isError: enabled && query.isError,
    markRecentRead: () => {
      if (user && enabled) {
        try {
          markRead(
            user.id,
            items.map((entry) => entry.event_id)
          )
        } catch {
          // The in-memory update still works when browser storage is unavailable.
        }
      }
    },
  }
}
