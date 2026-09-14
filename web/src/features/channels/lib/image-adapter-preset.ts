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
import type { ImageAdapterConfig } from './image-adapter'

/** Exact input sizes used by OpenAI-compatible clients, mapped to upstream tiers. */
const SIZE_PRESET: Record<string, [string, string]> = {
  '1024x1024': ['1080P', '1:1'],
  '1280x720': ['1080P', '16:9'],
  '720x1280': ['1080P', '9:16'],
  '1152x864': ['1080P', '4:3'],
  '864x1152': ['1080P', '3:4'],
  '1248x832': ['1080P', '3:2'],
  '832x1248': ['1080P', '2:3'],
  '1440x720': ['1080P', '2:1'],
  '720x1440': ['1080P', '1:2'],
  '1456x624': ['1080P', '21:9'],
  '2048x2048': ['2K', '1:1'],
  '2048x1152': ['2K', '16:9'],
  '1152x2048': ['2K', '9:16'],
  '2304x1728': ['2K', '4:3'],
  '1728x2304': ['2K', '3:4'],
  '2496x1664': ['2K', '3:2'],
  '1664x2496': ['2K', '2:3'],
  '2048x1024': ['2K', '2:1'],
  '1024x2048': ['2K', '1:2'],
  '3024x1296': ['2K', '21:9'],
  '2880x2880': ['4K', '1:1'],
  '3840x2160': ['4K', '16:9'],
  '2160x3840': ['4K', '9:16'],
  '3264x2448': ['4K', '4:3'],
  '2448x3264': ['4K', '3:4'],
  '3504x2336': ['4K', '3:2'],
  '2336x3504': ['4K', '2:3'],
  '3840x1920': ['4K', '2:1'],
  '1920x3840': ['4K', '1:2'],
  '3696x1584': ['4K', '21:9'],
}

export const NATIVE_ASYNC_IMAGE_ADAPTER_PRESET: ImageAdapterConfig = {
  models: [],
  request: {
    generations_path: '/v1/images/generations',
    edits_path: '/v1/images/generations',
    field_map: { quality: 'detail_level' },
    value_map: {
      quality: {
        low: 'Low',
        medium: 'Medium',
        high: 'High',
        xhigh: 'Extra High',
        max: 'Max',
      },
    },
    size_map: Object.fromEntries(
      Object.entries(SIZE_PRESET).map(([size, [quality, aspect_ratio]]) => [
        size,
        { quality, aspect_ratio },
      ])
    ),
    drop_fields: ['size', 'response_format', 'output_format'],
    reject_fields: ['mask', 'background'],
  },
  references: {
    field: 'content',
    encoding: 'image_url_content',
    max_images: 16,
    upload_local: true,
  },
  task: {
    id_paths: ['id', 'task_id'],
    status_path: 'status',
    result_url_path: 'data.0.url',
    result_b64_path: 'data.0.b64_json',
    error_path: 'error',
    poll_url_template: '/v1/images/generations/{task_id}',
    content_url_template: '/v1/images/{task_id}/content',
    pending_states: ['queued', 'in_progress'],
    success_states: ['completed'],
    failure_states: ['failed'],
    poll_interval_seconds: 4,
  },
}
