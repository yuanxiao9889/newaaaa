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
import { assert, describe, expect, test } from 'vitest'

import { applyFixedPriceUnitForModel } from '../model-pricing-core'
import {
  buildModelSnapshots,
  getModeLabel,
  getPriceDetail,
  getPriceSummary,
  type ModelPricingSnapshotInput,
} from '../model-pricing-snapshots'
import { applyPriceSyncSelections, applyPricingDraft, pricingOptions } from '@/features/model-pricing/pricing'

const emptySnapshotInput: ModelPricingSnapshotInput = {
  modelPrice: '{}',
  modelRatio: '{}',
  cacheRatio: '{}',
  createCacheRatio: '{}',
  completionRatio: '{}',
  imageRatio: '{}',
  audioRatio: '{}',
  audioCompletionRatio: '{}',
  billingMode: '{}',
  billingExpr: '{}',
  fixedPriceUnit: '{}',
}

describe('model pricing fixed price units', () => {
  test('snapshot loading prefers explicit units and infers unmigrated video models', () => {
    const rows = buildModelSnapshots({
      ...emptySnapshotInput,
      modelPrice: '{"sora-video":0.45,"legacy-video":0.3,"image-model":0.1}',
      fixedPriceUnit: '{"sora-video":"request"}',
    })
    const byName = new Map(rows.map((row) => [row.name, row]))

    assert.equal(byName.get('sora-video')?.fixedPriceUnit, 'request')
    assert.equal(byName.get('legacy-video')?.fixedPriceUnit, 'second')
    assert.equal(byName.get('image-model')?.fixedPriceUnit, 'request')
  })

  test('fixed unit map is written for fixed prices and cleared for other modes', () => {
    const units = { video: 'second' as const }

    applyFixedPriceUnitForModel(
      units,
      'video',
      'per-request',
      '0.45',
      'request'
    )
    expect(units).toEqual({ video: 'request' })

    applyFixedPriceUnitForModel(units, 'video', 'per-token', '0.45', 'second')
    expect(units).toEqual({})

    units.video = 'second'
    applyFixedPriceUnitForModel(units, 'video', 'tiered_expr', '0.45', 'second')
    expect(units).toEqual({})
  })

  test('table summaries identify per-second fixed prices', () => {
    const [row] = buildModelSnapshots({
      ...emptySnapshotInput,
      modelPrice: '{"video":0.45}',
      fixedPriceUnit: '{"video":"second"}',
    })
    const t = (key: string) => key

    assert.equal(
      getModeLabel(row.billingMode, row.fixedPriceUnit),
      'Per second'
    )
    assert.equal(getPriceSummary(row, t), '$0.45 / second')
    assert.equal(getPriceDetail(row, t), 'Fixed per second')
  })

  test('upstream price sync preserves the unit and changing mode clears it', () => {
    const selected = applyPriceSyncSelections(pricingOptions({}), { video: { model_price: 0.45, fixed_price_unit: 'second' } })
    expect(JSON.parse(selected.ModelPrice)).toEqual({ video: 0.45 })
    expect(JSON.parse(selected['billing_setting.fixed_price_unit'])).toEqual({ video: 'second' })
    const changed = applyPricingDraft(selected, { name: 'video', billingMode: 'per-token', ratio: '2' })
    expect(JSON.parse(changed.ModelPrice)).toEqual({})
    expect(JSON.parse(changed['billing_setting.fixed_price_unit'])).toEqual({})
  })
})
