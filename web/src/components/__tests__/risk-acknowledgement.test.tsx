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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { RiskAcknowledgementDialog } from '../risk-acknowledgement-dialog'

it('keeps duplicate confirmation inputs independent and preserves focus when static guidance is inserted', async () => {
  const user = userEvent.setup()
  const onConfirm = vi.fn()
  const parts = [
    {
      type: 'input' as const,
      text: 'CONFIRM',
      placeholder: 'First confirmation',
    },
    {
      type: 'input' as const,
      text: 'CONFIRM',
      placeholder: 'Second confirmation',
    },
  ]
  const props = {
    open: true,
    onOpenChange: vi.fn(),
    title: 'Confirm operation',
    description: 'Both confirmations are required.',
    requiredTextParts: parts,
    onConfirm,
  }
  const { rerender } = render(<RiskAcknowledgementDialog {...props} />)
  const first = screen.getByPlaceholderText('First confirmation')
  const second = screen.getByPlaceholderText('Second confirmation')
  const confirm = screen.getByRole('button', { name: 'Confirm' })
  await waitFor(() => expect(first).toHaveFocus())
  expect(confirm).toBeDisabled()
  await user.type(first, 'CONFIRM')
  expect(confirm).toBeDisabled()
  await user.type(second, 'CONFIRM')
  expect(confirm).toBeEnabled()

  rerender(
    <RiskAcknowledgementDialog
      {...props}
      requiredTextParts={[
        { type: 'static', text: 'Please verify both entries.' },
        ...parts,
      ]}
    />
  )
  expect(screen.getByPlaceholderText('Second confirmation')).toHaveFocus()
  expect(screen.getByPlaceholderText('First confirmation')).toHaveValue(
    'CONFIRM'
  )
  expect(screen.getByPlaceholderText('Second confirmation')).toHaveValue(
    'CONFIRM'
  )
  fireEvent.click(confirm)
  expect(onConfirm).toHaveBeenCalledOnce()
})
