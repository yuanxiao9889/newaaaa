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
import { zodResolver } from '@hookform/resolvers/zod'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { describe, expect, test, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { ImageAdapterSettings } from '../image-adapter-settings'

function TestForm(props: {
  disabled?: boolean
  onSubmit?: (values: ChannelFormValues) => void
  paramOverride?: string
}) {
  const form = useForm<ChannelFormValues>({
    resolver: zodResolver(channelFormSchema),
    defaultValues: {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Image upstream',
      models: 'image-model',
      param_override: props.paramOverride || '',
    },
  })
  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit((values) => props.onSubmit?.(values))}>
        <ImageAdapterSettings form={form} disabled={props.disabled === true} />
        <Button type='submit'>Save</Button>
      </form>
    </Form>
  )
}

describe('image adapter settings', () => {
  test('loading a template enables conversion and keeps the configuration after switching off', async () => {
    const submit = vi.fn()
    const user = userEvent.setup()
    render(<TestForm onSubmit={submit} />)

    await user.click(
      screen.getByRole('button', {
        name: 'Load native async public-reference template',
      })
    )
    expect(
      screen.getByRole('switch', { name: 'Image API conversion' })
    ).toBeChecked()
    const editor = await screen.findByRole('textbox', {
      name: 'Image conversion configuration (JSON)',
    })
    expect((editor as HTMLTextAreaElement).value).toContain('poll_url_template')
    screen.getByRole('switch', { name: 'Image API conversion' }).focus()
    await user.keyboard('[Space]')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(submit).toHaveBeenCalledWith(
        expect.objectContaining({
          image_adapter_enabled: false,
          image_adapter: expect.stringContaining('poll_url_template'),
        })
      )
    )
  })

  test('a locked form disables the conversion switch and template button', () => {
    render(<TestForm disabled />)

    expect(
      screen.getByRole('switch', { name: 'Image API conversion' })
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByRole('button', {
        name: 'Load native async public-reference template',
      })
    ).toBeDisabled()
  })

  test('invalid JSON reports a visible editor error and prevents submission', async () => {
    const submit = vi.fn()
    const user = userEvent.setup()
    render(<TestForm onSubmit={submit} />)
    await user.click(
      screen.getByRole('switch', { name: 'Image API conversion' })
    )
    const editor = await screen.findByRole('textbox', {
      name: 'Image conversion configuration (JSON)',
    })
    fireEvent.input(editor, { target: { value: '{broken' } })
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(
      await screen.findByText(
        'Image conversion configuration must be valid JSON'
      )
    ).toBeVisible()
    expect(editor).toHaveAttribute('aria-invalid', 'true')
    expect(submit).not.toHaveBeenCalled()
  })

  test('loading a template explains preserved conflicting parameter rules', async () => {
    const user = userEvent.setup()
    render(<TestForm paramOverride='{"async":false}' />)
    await user.click(
      screen.getByRole('button', {
        name: 'Load native async public-reference template',
      })
    )

    expect(
      screen.getByText(
        'Image template loaded. Existing rules for async were preserved; review async=true, n=1 and stream=false in Parameter Override'
      )
    ).toBeVisible()
  })
})
