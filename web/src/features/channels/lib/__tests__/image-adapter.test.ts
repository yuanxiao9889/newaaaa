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
import { describe, expect, test } from 'vitest'

import type { Channel } from '../../types'
import {
  buildSettingJSON,
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
} from '../channel-form'
import {
  mergeImageAdapterParameterOverride,
  parseImageAdapterConfig,
} from '../image-adapter'
import { NATIVE_ASYNC_IMAGE_ADAPTER_PRESET } from '../image-adapter-preset'

const channel = {
  ...CHANNEL_FORM_DEFAULT_VALUES,
  id: 1,
  name: 'Image upstream',
  models: 'image-model',
  group: 'default',
  channel_info: { multi_key_mode: 'random' },
} as Channel

describe('image adapter channel settings', () => {
  test('editing a saved adapter restores and preserves its configuration', () => {
    const imageAdapter = { ...NATIVE_ASYNC_IMAGE_ADAPTER_PRESET, enabled: true }
    const defaults = transformChannelToFormDefaults({
      ...channel,
      setting: JSON.stringify({ image_adapter: imageAdapter }),
    })

    expect(defaults).toMatchObject({ image_adapter_enabled: true })
    expect(JSON.parse(buildSettingJSON(defaults)).image_adapter).toEqual(
      imageAdapter
    )
  })

  test('disabling an adapter keeps its mapping available after reopening', () => {
    const form = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      image_adapter_enabled: false,
      image_adapter: JSON.stringify(NATIVE_ASYNC_IMAGE_ADAPTER_PRESET),
    }
    const setting = buildSettingJSON(form)
    const defaults = transformChannelToFormDefaults({ ...channel, setting })

    expect(JSON.parse(setting).image_adapter).toEqual({
      ...NATIVE_ASYNC_IMAGE_ADAPTER_PRESET,
      enabled: false,
    })
    expect(defaults).toMatchObject({ image_adapter_enabled: false })
    expect(JSON.parse(buildSettingJSON(defaults)).image_adapter).toEqual(
      JSON.parse(setting).image_adapter
    )
  })

  test('enabling without a configuration blocks save at the visible editor field', () => {
    const result = channelFormSchema.safeParse({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Image upstream',
      models: 'image-model',
      image_adapter_enabled: true,
    })

    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues).toContainEqual(
        expect.objectContaining({ path: ['image_adapter'] })
      )
    }
  })

  test('a disabled incomplete configuration can be saved and restored without dropping fields', () => {
    const form = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Image upstream',
      models: 'image-model',
      image_adapter_enabled: false,
      image_adapter:
        '{"request":{"field_map":{"quality":"detail_level"}},"task":{}}',
    }

    expect(channelFormSchema.safeParse(form).success).toBe(true)
    const defaults = transformChannelToFormDefaults({
      ...channel,
      setting: buildSettingJSON(form),
    })
    expect(JSON.parse(defaults.image_adapter || '{}')).toEqual(
      JSON.parse(form.image_adapter)
    )
  })

  test('the preset maps 30 exact sizes and uses portable task paths', () => {
    const config = parseImageAdapterConfig(
      JSON.stringify(NATIVE_ASYNC_IMAGE_ADAPTER_PRESET)
    )

    expect(Object.keys(config?.request.size_map || {})).toHaveLength(30)
    expect(config?.request.size_map?.['1024x1024']).toEqual({
      quality: '1080P',
      aspect_ratio: '1:1',
    })
    expect(config?.request.size_map?.['2880x2880']).toEqual({
      quality: '4K',
      aspect_ratio: '1:1',
    })
    expect(config?.models).toEqual([])
  })

  test('rejects nested request field names before saving', () => {
    expect(() =>
      parseImageAdapterConfig(
        JSON.stringify({ request: { field_map: { quality: 'image.detail' } } })
      )
    ).toThrow()
  })
})

describe('image adapter template parameter rules', () => {
  test('adds image-only constants while preserving legacy fields and custom operations', () => {
    const operation = { path: 'temperature', mode: 'set', value: 0.2 }
    const result = mergeImageAdapterParameterOverride(
      JSON.stringify({ seed: 5, operations: [operation] })
    )
    const merged = JSON.parse(result.value)

    expect(merged.seed).toBe(5)
    expect(merged.operations[0]).toEqual(operation)
    expect(merged.operations.slice(1)).toEqual(
      ['async', 'n', 'stream'].map((path) =>
        expect.objectContaining({
          path,
          logic: 'AND',
          conditions: [
            { path: 'request_path', mode: 'prefix', value: '/v1/images/' },
            { path: 'image_adapter_enabled', mode: 'full', value: true },
          ],
        })
      )
    )
  })

  test('preserves conflicting legacy and operation rules for administrator review', () => {
    const operation = { path: 'n', mode: 'set', value: 2 }
    const result = mergeImageAdapterParameterOverride(
      JSON.stringify({ async: false, operations: [operation] })
    )

    expect(result.preservedFields).toEqual(['async', 'n'])
    expect(JSON.parse(result.value)).toMatchObject({
      async: false,
      operations: [operation, { path: 'stream', value: false }],
    })
  })

  test('applying the template twice does not duplicate its parameter operations', () => {
    const first = mergeImageAdapterParameterOverride('')
    const second = mergeImageAdapterParameterOverride(first.value)

    expect(JSON.parse(second.value)).toEqual(JSON.parse(first.value))
  })

  test('rejects malformed operations without replacing the existing configuration', () => {
    expect(() =>
      mergeImageAdapterParameterOverride('{"operations":{}}')
    ).toThrow()
    expect(() =>
      mergeImageAdapterParameterOverride('{"operations":[{}]}')
    ).toThrow()
  })
})
