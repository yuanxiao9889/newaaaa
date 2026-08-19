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
import { assert, describe, test } from 'vitest'

import {
  isSecondBasedFixedPriceModel,
  resolveFixedPriceUnit,
} from '../model-helpers'

describe('fixed price unit compatibility', () => {
  test('explicit units take precedence over legacy video inference', () => {
    assert.equal(
      resolveFixedPriceUnit({
        model_name: 'sora-video',
        fixed_price_unit: 'request',
      }),
      'request'
    )
    assert.equal(
      resolveFixedPriceUnit({
        model_name: 'ordinary-model',
        fixed_price_unit: 'second',
      }),
      'second'
    )
  })

  test('unmigrated video models and video endpoints remain per second', () => {
    assert.equal(
      resolveFixedPriceUnit({ model_name: 'vendor-veo-fast' }),
      'second'
    )
    assert.equal(
      resolveFixedPriceUnit({
        model_name: 'custom-model',
        supported_endpoint_types: ['openai-video'],
      }),
      'second'
    )
    assert.equal(
      resolveFixedPriceUnit({ model_name: 'ordinary-model' }),
      'request'
    )
  })

  test('token-priced models never display a fixed per-second unit', () => {
    assert.equal(
      isSecondBasedFixedPriceModel({
        id: 1,
        model_name: 'video-token-model',
        quota_type: 0,
        model_ratio: 1,
        completion_ratio: 1,
        enable_groups: [],
        fixed_price_unit: 'second',
      }),
      false
    )
  })
})
