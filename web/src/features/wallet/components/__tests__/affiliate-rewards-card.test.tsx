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
import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'

import type { UserWalletData } from '../../types'
import { AffiliateRewardsCard } from '../affiliate-rewards-card'

const user: UserWalletData = {
  id: 1,
  username: 'inviter',
  quota: 1000,
  used_quota: 0,
  request_count: 0,
  aff_quota: 0,
  aff_history_quota: 0,
  aff_count: 1,
  group: 'default',
}

it('updates the reward limit explanation when VIP membership changes', () => {
  const props = {
    user,
    affiliateLink: 'https://example.test/register?aff=inviter',
    onTransfer: () => undefined,
  }
  const view = render(<AffiliateRewardsCard {...props} />)
  expect(
    screen.getByText(
      'Rewards are granted only for the first 5 successful top-ups from each invited user.'
    )
  ).toBeInTheDocument()
  view.rerender(
    <AffiliateRewardsCard {...props} user={{ ...user, is_vip: true }} />
  )
  expect(
    screen.getByText(
      'VIP benefit: referral rewards have no count limit per invited user.'
    )
  ).toBeInTheDocument()
  expect(
    screen.queryByText(
      'Rewards are granted only for the first 5 successful top-ups from each invited user.'
    )
  ).not.toBeInTheDocument()
  expect(
    screen.getByText(
      'Inviter reward is 10% of each invited user wallet top-up payment.'
    )
  ).toBeInTheDocument()
})
