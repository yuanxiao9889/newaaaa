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
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Badge } from '@/components/ui/badge'

import { CodeExample } from './code-example'
import { ContractParameters } from './contract-parameters'
import { generationContracts } from './generation-data'
import { buildGenerationMarkdown } from './generation-guide'
import { buildJsonRequestSamples } from './request-samples'

export function IntegrationExport({ baseUrl }: { baseUrl: string }) {
  const { t } = useTranslation()
  const markdown = buildGenerationMarkdown(baseUrl, t)
  return (
    <div
      id='ai-integration'
      className='bg-muted/25 mt-8 scroll-mt-32 space-y-3 rounded-xl border p-5'
    >
      <h2 className='font-semibold'>{t('Integration guide for AI')}</h2>
      <p className='text-muted-foreground text-sm leading-6'>
        {t(
          'This export covers Seed Audio, the independent Midjourney image model and the video models below.'
        )}
      </p>
      <div className='flex flex-wrap items-center gap-4'>
        <CopyButton
          value={markdown}
          variant='outline'
          size='sm'
          aria-label={t('Copy for AI')}
        >
          {t('Copy for AI')}
        </CopyButton>
        <a
          className='text-primary text-sm underline underline-offset-4'
          download='oopii-model-integration.md'
          href={`data:text/markdown;charset=utf-8,${encodeURIComponent(markdown)}`}
        >
          {t('Download Markdown')}
        </a>
      </div>
    </div>
  )
}

export function GenerationDocs({ baseUrl }: { baseUrl: string }) {
  const { t } = useTranslation()
  return generationContracts.map((contract) => (
    <section
      key={contract.id}
      aria-labelledby={contract.id}
      className='border-border/70 mt-16 space-y-6 border-t pt-12'
    >
      <div className='space-y-3'>
        <Badge variant='secondary'>{contract.model}</Badge>
        <h2 id={contract.id} className='scroll-mt-32 text-2xl font-semibold'>
          {t(contract.title)}
        </h2>
        <p className='text-muted-foreground leading-7'>
          {t(contract.description)}
        </p>
      </div>
      <div className='space-y-2 rounded-xl border p-4'>
        {contract.endpoints.map((endpoint) => (
          <div key={endpoint.path} className='flex items-start gap-3 text-sm'>
            <Badge
              variant={endpoint.method === 'POST' ? 'default' : 'secondary'}
            >
              {endpoint.method}
            </Badge>
            <code className='min-w-0 leading-6 break-all'>{endpoint.path}</code>
          </div>
        ))}
        <p className='text-muted-foreground pt-2 font-mono text-xs'>
          Authorization: Bearer $NEW_API_KEY · Content-Type: application/json
        </p>
      </div>
      <h3 className='text-lg font-semibold'>{t('Request parameters')}</h3>
      <ContractParameters fields={contract.parameters} />
      <ul className='bg-muted/25 list-disc space-y-2 rounded-xl border py-4 pr-5 pl-9 text-sm leading-7'>
        {contract.notes.map((note) => (
          <li key={note}>{t(note)}</li>
        ))}
      </ul>
      {contract.examples.map((example) => (
        <CodeExample
          key={example.path + example.title}
          title={t(example.title)}
          samples={buildJsonRequestSamples(baseUrl, example.body, example.path)}
        />
      ))}
      <h3 className='text-lg font-semibold'>{t('Response examples')}</h3>
      {contract.responses.map((response) => (
        <pre
          key={JSON.stringify(response)}
          className='bg-muted/30 overflow-x-auto rounded-xl border p-4 text-xs leading-6'
        >
          <code>{JSON.stringify(response, null, 2)}</code>
        </pre>
      ))}
    </section>
  ))
}
