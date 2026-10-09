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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { HeroTerminalDemo } from '../hero-terminal-demo'

describe('homepage request examples', () => {
  it('keeps copy and long request text keyboard-accessible after protocol selection', async () => {
    const user = userEvent.setup()
    render(<HeroTerminalDemo />)

    await user.tab()
    await user.keyboard('{End}{Enter}')
    expect(screen.getByRole('tab', { name: 'Gemini' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    await user.tab()
    expect(screen.getByRole('tabpanel', { name: 'Gemini' })).toHaveFocus()
    await user.tab()
    expect(
      screen.getByRole('button', { name: 'Copy request example' })
    ).toHaveFocus()
    await user.tab()
    const request = within(
      screen.getByRole('tabpanel', { name: 'Gemini' })
    ).getByLabelText('Request example')
    expect(request).toHaveFocus()
    expect(request).toHaveTextContent(
      '/v1beta/models/YOUR_MODEL:generateContent'
    )
  })

  it('supports keyboard protocol selection and exposes the matching request', async () => {
    const user = userEvent.setup()
    render(<HeroTerminalDemo />)

    await user.tab()
    expect(screen.getByRole('tab', { name: 'OpenAI' })).toHaveFocus()
    await user.keyboard('{ArrowRight}{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Claude' })).toHaveFocus()
    await user.keyboard('{Enter}')

    expect(screen.getByRole('tab', { name: 'Claude' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    const panel = screen.getByRole('tabpanel', { name: 'Claude' })
    expect(panel).toHaveTextContent('/v1/messages')
    expect(panel).toHaveTextContent('anthropic-version: 2023-06-01')
    expect(panel).toHaveTextContent('"max_tokens": 1024')
    expect(
      screen.queryByRole('tabpanel', { name: 'OpenAI' })
    ).not.toBeInTheDocument()
  })

  it.each([
    {
      protocol: 'Responses',
      endpoint: '/v1/responses',
      header: 'Authorization: Bearer YOUR_API_KEY',
    },
    {
      protocol: 'OpenAI',
      endpoint: '/v1/chat/completions',
      header: 'Authorization: Bearer YOUR_API_KEY',
    },
    {
      protocol: 'Claude',
      endpoint: '/v1/messages',
      header: 'x-api-key: YOUR_API_KEY',
    },
    {
      protocol: 'Gemini',
      endpoint: '/v1beta/models/YOUR_MODEL:generateContent',
      header: 'x-goog-api-key: YOUR_API_KEY',
    },
  ])(
    'copies the visible $protocol example with its endpoint and headers',
    async (example) => {
      const user = userEvent.setup()
      const writeText = vi.spyOn(navigator.clipboard, 'writeText')
      render(<HeroTerminalDemo />)

      await user.click(screen.getByRole('tab', { name: example.protocol }))
      const panel = screen.getByRole('tabpanel', { name: example.protocol })
      const request =
        within(panel).getByLabelText('Request example').textContent
      expect(request).toContain(example.endpoint)
      expect(request).toContain(example.header)
      expect(request).toContain('Content-Type: application/json')
      await user.click(
        within(panel).getByRole('button', { name: 'Copy request example' })
      )

      expect(writeText).toHaveBeenCalledWith(request)
      expect(
        await within(panel).findByRole('button', { name: 'Copied' })
      ).toBeVisible()
    }
  )
})
