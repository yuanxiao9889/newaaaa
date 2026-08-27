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
import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { ImageDialog } from '../image-dialog'

describe('image preview dialog', () => {
  test('uses the image URL only as the image source without displaying it as text', () => {
    const imageUrl = 'https://example.com/mj/image/task-123'

    render(
      <ImageDialog
        imageUrl={imageUrl}
        taskId='task-123'
        open
        onOpenChange={() => undefined}
      />
    )

    expect(screen.getByRole('img', { name: 'Generated image' })).toHaveAttribute(
      'src',
      imageUrl
    )
    expect(document.body).not.toHaveTextContent(imageUrl)
  })
})
