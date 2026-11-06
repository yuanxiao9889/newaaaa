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

import {
  cleanBackupCode,
  formatBackupCode,
  isValidBackupCode,
} from '../validation'

describe('backup code normalization', () => {
  it.each([
    ['', ''],
    ['ab-cd ef!12', 'ABCD-EF12'],
    ['abcd', 'ABCD'],
    ['abcdef123456', 'ABCD-EF12'],
  ])(
    'formats %j as %j without changing the eight-character limit',
    (input, expected) => {
      expect(formatBackupCode(input)).toBe(expected)
    }
  )

  it('removes every hyphen before submission while preserving the remaining characters', () => {
    expect(cleanBackupCode('-AB-CD--EF12-')).toBe('ABCDEF12')
    expect(cleanBackupCode('AB CD!')).toBe('AB CD!')
  })

  it('accepts a complete formatted code and rejects incomplete or punctuated codes', () => {
    expect(isValidBackupCode(formatBackupCode('abcd ef12'))).toBe(true)
    expect(isValidBackupCode('ABCD-EF1')).toBe(false)
    expect(isValidBackupCode('ABCD-EF!2')).toBe(false)
  })
})
