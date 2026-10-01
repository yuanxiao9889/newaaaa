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

import { getChannelConfigurationState } from '../../lib/channel-configuration'
import {
  buildSettingJSON,
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { GPT_IMAGE_4K_FILTER_PRESET } from '../../lib/image-request-filter'
import { channelSchema } from '../../types'
import { ImageRequestFilterSettings } from '../image-request-filter-settings'

const defaultValues: ChannelFormValues = {
  ...CHANNEL_FORM_DEFAULT_VALUES,
  name: 'Filtered image upstream',
  models: 'gpt-image-2,gpt-image-2.5-sunburst,gpt-image-2.5-flare',
  param_override: '{"async":true}',
}

function TestForm(props: {
  disabled?: boolean
  values?: Partial<ChannelFormValues>
  onSubmit?: (values: ChannelFormValues) => void
}) {
  const form = useForm<ChannelFormValues>({
    resolver: zodResolver(channelFormSchema),
    defaultValues: { ...defaultValues, ...props.values },
  })
  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit((values) => props.onSubmit?.(values))}>
        <ImageRequestFilterSettings
          form={form}
          disabled={props.disabled === true}
        />
        <Button type='submit'>Save</Button>
      </form>
    </Form>
  )
}

describe('image request filter settings', () => {
  test('loading the preset enables only the exact quality and 4K size rules without changing parameter overrides', async () => {
    const submit = vi.fn()
    const user = userEvent.setup()
    render(<TestForm onSubmit={submit} />)

    const filterSwitch = screen.getByRole('switch', {
      name: 'Image request filtering',
    })
    expect(filterSwitch).not.toBeChecked()
    expect(
      screen.queryByRole('textbox', {
        name: 'Image request filter rules (JSON)',
      })
    ).not.toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: 'Load GPT Image 4K preset' })
    )
    expect(filterSwitch).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(submit).toHaveBeenCalled())

    const saved = submit.mock.calls[0][0] as ChannelFormValues
    const filter = JSON.parse(buildSettingJSON(saved)).image_request_filter
    const sizes = [
      '2880x2880',
      '3200x2560',
      '2160x3840',
      '3696x1584',
      '1584x3696',
      '1280x3840',
      '3840x1280',
      '1920x3840',
      '3840x1920',
      '3840x2160',
      '3264x2448',
      '3504x2336',
      '2560x3200',
      '2448x3264',
      '2336x3504',
    ]
    expect(filter).toEqual({
      enabled: true,
      rules: [
        { models: ['gpt-image-2'], qualities: ['medium', 'high'], sizes },
        {
          models: ['gpt-image-2.5-sunburst', 'gpt-image-2.5-flare'],
          qualities: ['high', 'xhigh', 'max'],
          sizes,
        },
      ],
    })
    expect(saved.param_override).toBe(defaultValues.param_override)
    expect(getChannelConfigurationState(saved, {}, true).sections.other).toBe(
      'configured'
    )
  })

  test('switching off with the keyboard preserves rules after saving and reopening the channel', async () => {
    const submit = vi.fn()
    const user = userEvent.setup()
    render(
      <TestForm
        onSubmit={submit}
        values={{
          image_request_filter_enabled: true,
          image_request_filter: JSON.stringify(GPT_IMAGE_4K_FILTER_PRESET),
        }}
      />
    )
    screen.getByRole('switch', { name: 'Image request filtering' }).focus()
    await user.keyboard('[Space]')
    expect(
      screen.getByRole('switch', { name: 'Image request filtering' })
    ).not.toBeChecked()
    expect(
      screen.getByRole('textbox', { name: 'Image request filter rules (JSON)' })
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(submit).toHaveBeenCalled())

    const saved = submit.mock.calls[0][0] as ChannelFormValues
    const setting = buildSettingJSON(saved)
    const channel = channelSchema.parse({
      id: 136,
      name: defaultValues.name,
      type: 1,
      key: '',
      status: 1,
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      models: defaultValues.models,
      group: 'default',
      setting,
    })
    const reopened = transformChannelToFormDefaults(channel)
    expect(reopened.image_request_filter_enabled).toBe(false)
    expect(JSON.parse(buildSettingJSON(reopened)).image_request_filter).toEqual(
      {
        ...GPT_IMAGE_4K_FILTER_PRESET,
        enabled: false,
      }
    )
  })

  test('a locked channel disables the switch, preset button, and saved rule editor', () => {
    render(
      <TestForm
        disabled
        values={{
          image_request_filter: JSON.stringify(GPT_IMAGE_4K_FILTER_PRESET),
        }}
      />
    )
    expect(
      screen.getByRole('switch', { name: 'Image request filtering' })
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByRole('button', { name: 'Load GPT Image 4K preset' })
    ).toBeDisabled()
    expect(
      screen.getByRole('textbox', { name: 'Image request filter rules (JSON)' })
    ).toBeDisabled()
  })

  test('invalid JSON shows an accessible editor error and blocks saving', async () => {
    const submit = vi.fn()
    const user = userEvent.setup()
    render(<TestForm onSubmit={submit} />)
    await user.click(
      screen.getByRole('switch', { name: 'Image request filtering' })
    )
    const editor = await screen.findByRole('textbox', {
      name: 'Image request filter rules (JSON)',
    })
    fireEvent.input(editor, { target: { value: '{broken' } })
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(
      await screen.findByText(
        'Image request filter configuration must be valid JSON'
      )
    ).toBeVisible()
    expect(editor).toHaveAttribute('aria-invalid', 'true')
    expect(submit).not.toHaveBeenCalled()
  })

  test.each([
    ['missing configuration', ''],
    ['no rules', '{"rules":[]}'],
    [
      'missing qualities',
      '{"rules":[{"models":["gpt-image-2"],"sizes":["2880x2880"]}]}',
    ],
    [
      'auto quality',
      '{"rules":[{"models":["gpt-image-2"],"qualities":["auto"],"sizes":["2880x2880"]}]}',
    ],
    [
      'ambiguous models',
      '{"rules":[{"models":["gpt-image-2"],"qualities":["high"],"sizes":["2880x2880"]},{"models":["gpt-image-2"],"qualities":["medium"],"sizes":["2880x2880"]}]}',
    ],
    [
      'invalid size',
      '{"rules":[{"models":["gpt-image-2"],"qualities":["high"],"sizes":["4K"]}]}',
    ],
  ])('%s prevents enabling the filter', (_name, configuration) => {
    const result = channelFormSchema.safeParse({
      ...defaultValues,
      image_request_filter_enabled: true,
      image_request_filter: configuration,
    })
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues).toContainEqual(
        expect.objectContaining({ path: ['image_request_filter'] })
      )
    }
  })

  test('disabling an incomplete draft allows it to be saved without dropping its rules', () => {
    const values = {
      ...defaultValues,
      image_request_filter_enabled: false,
      image_request_filter: '{"rules":[{"models":["gpt-image-2"]}]}',
    }
    expect(channelFormSchema.safeParse(values).success).toBe(true)
    expect(JSON.parse(buildSettingJSON(values)).image_request_filter).toEqual({
      enabled: false,
      rules: [{ models: ['gpt-image-2'] }],
    })
  })
})
