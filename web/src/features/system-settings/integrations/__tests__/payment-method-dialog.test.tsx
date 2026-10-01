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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { PaymentMethodDialog } from '../payment-method-dialog'

it('selects a payment type and saves a custom type without submitting on Enter', async () => {
  const save = vi.fn()
  render(<PaymentMethodDialog open onOpenChange={vi.fn()} onSave={save} />)
  const user = userEvent.setup()
  const submit = screen.getByRole('button', { name: 'Add' })
  const type = screen.getByRole('combobox', { name: 'Payment type key' })
  await user.click(type)
  await user.click(screen.getByRole('option', { name: 'Stripe (stripe)' }))
  expect(type).toHaveValue('Stripe (stripe)')
  expect(
    screen.getByRole('textbox', { name: 'Name' })
  ).toHaveValue('Stripe')
  await user.clear(type)
  await user.type(type, 'custom-payment')
  await user.keyboard('{Enter}')
  expect(save).not.toHaveBeenCalled()
  expect(type).toHaveValue('custom-payment')
  await user.click(submit)
  await waitFor(() =>
    expect(save).toHaveBeenCalledWith({
      name: 'Stripe',
      type: 'custom-payment',
      icon: 'SiStripe',
    })
  )
})
