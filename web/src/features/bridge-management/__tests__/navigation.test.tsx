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
import { renderHook } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { useSidebarData } from '@/hooks/use-sidebar-data'
import { ROLE } from '@/lib/roles'
import { Route } from '@/routes/_authenticated/bridge-management'
import { useAuthStore } from '@/stores/auth-store'

afterEach(() => useAuthStore.getState().auth.reset())

it('places bridge management between task plugins and system settings with admin access', () => {
  const { result } = renderHook(() => useSidebarData())
  const items =
    result.current.navGroups.find((group) => group.id === 'admin')?.items ?? []
  const index = items.findIndex(
    (item) => 'url' in item && item.url === '/bridge-management'
  )
  expect(items[index - 1]).toMatchObject({ url: '/task-plugins' })
  expect(items[index]).toMatchObject({ requiredRole: ROLE.ADMIN })
  expect(items[index + 1]).toMatchObject({ url: '/system-settings/site' })
})

it.each([ROLE.GUEST, ROLE.USER])(
  'denies direct route access for role %s',
  (role) => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'user', role })
    expect(() => Route.options.beforeLoad?.({} as never)).toThrow()
  }
)

it.each([ROLE.ADMIN, ROLE.SUPER_ADMIN])(
  'allows direct route access for role %s',
  (role) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'administrator', role })
    expect(() => Route.options.beforeLoad?.({} as never)).not.toThrow()
  }
)
