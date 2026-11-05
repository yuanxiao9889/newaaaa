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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, cleanup } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { SidebarProvider } from '@/components/ui/sidebar'

import { NavGroup } from '../components/nav-group'

afterEach(cleanup)

it('an administrator alert badge is labelled and uses the destructive variant on the audit link', async () => {
  const root = createRootRoute({
    component: () => (
      <SidebarProvider>
        <NavGroup
          title='Logs'
          items={[
            {
              title: 'Audit Logs',
              url: '/usage-logs/audit',
              badge: '2',
              badgeVariant: 'destructive',
              badgeLabel: 'Unread administrator alerts',
            },
          ]}
        />
      </SidebarProvider>
    ),
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(<RouterProvider router={router} />)
  const link = await screen.findByRole('link', { name: /Audit Logs/ })
  expect(link).toHaveAttribute('href', '/usage-logs/audit')
  const badges = screen.getAllByLabelText('Unread administrator alerts')
  expect(badges.find((badge) => badge.textContent === '2')).toHaveAttribute(
    'data-variant',
    'destructive'
  )
  expect(badges.find((badge) => badge.textContent === '')).toHaveClass(
    'bg-destructive'
  )
})
