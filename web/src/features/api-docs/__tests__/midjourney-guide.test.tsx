import { render, screen, fireEvent } from '@testing-library/react'
import i18next from 'i18next'
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
import { describe, expect, it } from 'vitest'

import zh from '../../../i18n/locales/zh.json'
import { MidjourneyCanvasGuide } from '../midjourney-canvas-guide'

describe('Midjourney canvas documentation', () => {
  it('explains the omitted-speed 503 and shows the explicit fast request requirement', () => {
    render(<MidjourneyCanvasGuide baseUrl='https://relay.test' />)
    expect(screen.getByRole('note')).toHaveTextContent(
      'Set speedMode explicitly: "fast"'
    )
    expect(
      screen.getByRole('heading', {
        name: '503: No available channel for model mj_imagine',
      })
    ).toBeVisible()
    expect(
      screen.getByText(/Omitting speedMode does not default to fast/)
    ).toBeVisible()
  })

  it('shows modal instructions and switches the copyable example to JavaScript', () => {
    render(<MidjourneyCanvasGuide baseUrl='https://relay.test' />)
    expect(
      screen.getByRole('heading', { name: 'Start with a text-only request' })
    ).toBeVisible()
    expect(
      screen.getByRole('heading', { name: 'Button actions and modal editing' })
    ).toBeVisible()
    fireEvent.click(screen.getByRole('tab', { name: 'JavaScript' }))
    expect(
      screen.getByText(/const baseURL = 'https:\/\/relay.test'/)
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Copy code' })).toBeEnabled()
  })

  it('shows translated canvas guidance when Chinese is selected', async () => {
    i18next.addResourceBundle('zh', 'translation', zh)
    await i18next.changeLanguage('zh')
    try {
      render(<MidjourneyCanvasGuide baseUrl='https://relay.test' />)
      expect(
        screen.getByRole('heading', { name: 'Midjourney：按画布工作流接入' })
      ).toBeVisible()
      expect(
        screen.getByRole('heading', { name: '按钮动作与二次编辑提交' })
      ).toBeVisible()
      expect(screen.getByRole('note')).toHaveTextContent(
        '请明确设置 speedMode: "fast"'
      )
    } finally {
      await i18next.changeLanguage('en')
    }
  })
})
