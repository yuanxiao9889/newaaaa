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
import {
  ChevronRight,
  Code2,
  FileJson,
  ImageIcon,
  Layers3,
  Sparkles,
  Workflow,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

import { imageModels } from './data'
import { generationContracts } from './generation-data'
import { midjourneyNavItems } from './midjourney-data'
import { videoGroups } from './video-data'

const oopiiReferenceItems = [
  ['oopii-model-catalog', 'Current upstream model list'],
  ['text-api', 'Text and multimodal API routes'],
  ['video-api', 'Video task API'],
] as const

const referenceItems = [
  ['models-api', 'Query available models'],
  ['async-images', 'Asynchronous image tasks'],
  ['image-compression', 'Image compression and upload'],
  ['size-mapping', 'Size mapping'],
  ['response-format', 'Response format'],
  ['errors', 'Errors and status codes'],
] as const

export function DocsNavigation({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const groups = [
    {
      label: 'Get started',
      icon: Sparkles,
      items: [
        ['overview', 'Overview'],
        ['authentication', 'Authentication'],
        ['ai-integration', 'Integration guide for AI'],
      ],
    },
    {
      label: 'OOpii upstream model catalog',
      icon: FileJson,
      items: oopiiReferenceItems,
    },
    {
      label: 'Audio and independent image models',
      icon: Sparkles,
      items: generationContracts.map((contract) => [
        contract.id,
        contract.title,
      ]),
    },
    {
      label: 'apiDocs.video.title',
      icon: Workflow,
      items: videoGroups.map((group) => [group.id, group.title]),
    },
    {
      label: 'OpenAI Images models',
      icon: ImageIcon,
      items: imageModels
        .filter((model) => model.family === 'openai')
        .map((model) => [`model-${model.id}`, model.title ?? model.model]),
    },
    {
      label: 'OOpii image models',
      icon: Sparkles,
      items: imageModels
        .filter((model) => model.family === 'oopii')
        .map((model) => [`model-${model.id}`, model.title ?? model.model]),
    },
    {
      label: 'Gemini image models',
      icon: Layers3,
      items: imageModels
        .filter((model) => model.family === 'gemini')
        .map((model) => [`model-${model.id}`, model.title ?? model.model]),
    },
    {
      label: 'Midjourney task API',
      icon: Workflow,
      items: midjourneyNavItems,
    },
    {
      label: 'API reference',
      icon: Code2,
      items: referenceItems,
    },
  ]

  const search = query.trim().toLowerCase()
  const visibleGroups = groups
    .map((group) => ({
      ...group,
      items: group.items.filter(([id, label]) => {
        const videoModels =
          videoGroups
            .find((video) => video.id === id)
            ?.variants.flatMap((variant) => variant.models)
            .join(' ') ?? ''
        const generation = generationContracts.find(
          (contract) => contract.id === id
        )
        const image = imageModels.find((model) => `model-${model.id}` === id)
        const endpoints =
          generation?.endpoints.map((endpoint) => endpoint.path).join(' ') ?? ''
        const videoEndpoint = id.startsWith('video-') ? '/v1/videos' : ''
        const imageKeywords = image
          ? `${image.model} ${image.modelIds?.join(' ') ?? ''} ${image.endpoints.map((endpoint) => endpoint.path).join(' ')}`
          : ''
        return `${t(group.label)} ${t(label)} ${videoModels} ${generation?.model ?? ''} ${endpoints} ${videoEndpoint} ${imageKeywords}`
          .toLowerCase()
          .includes(search)
      }),
    }))
    .filter((group) => group.items.length > 0)
  return (
    <nav
      className='space-y-6 px-4 py-5'
      aria-label={t('API documentation navigation')}
    >
      <Input
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        aria-label={t('Search models and endpoints')}
        placeholder={t('Search models and endpoints')}
      />
      {visibleGroups.length === 0 && (
        <p role='status' className='text-muted-foreground px-2 text-sm'>
          {t('No matching documentation')}
        </p>
      )}
      {visibleGroups.map((group) => {
        const Icon = group.icon
        return (
          <div key={group.label}>
            <div className='text-muted-foreground mb-2 flex items-center gap-2 px-2 text-[11px] font-semibold tracking-[0.12em] uppercase'>
              <Icon className='size-3.5' />
              {t(group.label)}
            </div>
            <div className='grid gap-0.5'>
              {group.items.map(([id, label]) => (
                <a
                  key={id}
                  href={`#${id}`}
                  onClick={onNavigate}
                  className={cn(
                    'text-muted-foreground hover:bg-muted hover:text-foreground flex min-w-0 items-center gap-2 rounded-lg px-2 py-2 text-sm transition-colors',
                    id.startsWith('model-') && 'font-mono text-xs'
                  )}
                >
                  <ChevronRight className='size-3 shrink-0 opacity-50' />
                  <span className='truncate'>{t(label)}</span>
                </a>
              ))}
            </div>
          </div>
        )
      })}
    </nav>
  )
}
