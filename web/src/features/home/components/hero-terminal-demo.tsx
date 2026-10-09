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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'

interface HeroTerminalDemoProps {
  className?: string
}

const API_EXAMPLES = [
  {
    id: 'openai',
    label: 'OpenAI',
    endpoint: '/v1/chat/completions',
    headers: { Authorization: 'Bearer YOUR_API_KEY' },
    body: {
      model: 'YOUR_MODEL',
      messages: [{ role: 'user', content: 'Hello' }],
      stream: true,
    },
  },
  {
    id: 'responses',
    label: 'Responses',
    endpoint: '/v1/responses',
    headers: { Authorization: 'Bearer YOUR_API_KEY' },
    body: { model: 'YOUR_MODEL', input: 'Hello', stream: true },
  },
  {
    id: 'claude',
    label: 'Claude',
    endpoint: '/v1/messages',
    headers: { 'x-api-key': 'YOUR_API_KEY', 'anthropic-version': '2023-06-01' },
    body: {
      model: 'YOUR_MODEL',
      max_tokens: 1024,
      messages: [{ role: 'user', content: 'Hello' }],
    },
  },
  {
    id: 'gemini',
    label: 'Gemini',
    endpoint: '/v1beta/models/YOUR_MODEL:generateContent',
    headers: { 'x-goog-api-key': 'YOUR_API_KEY' },
    body: { contents: [{ parts: [{ text: 'Hello' }] }] },
  },
]

export function HeroTerminalDemo(props: HeroTerminalDemoProps) {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState<string>('openai')

  return (
    <div
      className={cn(
        'home-terminal min-w-0 border border-border bg-background',
        props.className
      )}
    >
      <div className='border-border flex items-center justify-between gap-3 border-b px-5 py-4'>
        <span className='text-foreground font-mono text-xs tracking-widest'>
          API // {t('Request example')}
        </span>
        <span className='flex gap-1.5' aria-hidden='true'>
          <span className='bg-primary size-1.5' />
          <span className='bg-primary/40 size-1.5' />
          <span className='bg-primary/20 size-1.5' />
        </span>
      </div>
      <Tabs
        value={activeTab}
        onValueChange={(value) => setActiveTab(String(value))}
        className='gap-0'
      >
        <TabsList
          variant='line'
          aria-label={t('API protocol')}
          className='border-border max-w-full justify-start overflow-x-auto border-b px-3 py-0'
        >
          {API_EXAMPLES.map((example) => (
            <TabsTrigger
              key={example.id}
              value={example.id}
              className='h-10 flex-none rounded-none px-2.5 font-mono text-xs sm:px-4'
            >
              {example.label}
            </TabsTrigger>
          ))}
        </TabsList>
        {API_EXAMPLES.map((example) => {
          const request = [
            `curl -X POST "https://YOUR_GATEWAY${example.endpoint}"`,
            ...Object.entries(example.headers).map(
              ([key, value]) => `  -H "${key}: ${value}"`
            ),
            '  -H "Content-Type: application/json"',
            `  -d '${JSON.stringify(example.body, null, 2)}'`,
          ].join(' \\\n')

          return (
            <TabsContent
              key={example.id}
              value={example.id}
              className='min-w-0'
            >
              <div className='border-border flex min-w-0 items-center gap-3 border-b px-5 py-3'>
                <span className='text-primary font-mono text-[10px]'>POST</span>
                <code
                  className='min-w-0 flex-1 truncate text-[11px]'
                  title={example.endpoint}
                >
                  {example.endpoint}
                </code>
                <CopyButton
                  value={request}
                  aria-label={t('Copy request example')}
                  className='size-11 rounded-none'
                  iconClassName='text-current'
                />
              </div>
              <pre
                className='text-foreground/80 m-0 min-h-80 overflow-x-auto p-5 font-mono text-[11px] leading-6 sm:text-xs'
                tabIndex={0}
                aria-label={t('Request example')}
              >
                <code>{request}</code>
              </pre>
            </TabsContent>
          )
        })}
      </Tabs>
      <p className='border-border text-muted-foreground border-t px-5 py-4 text-xs leading-5'>
        {t(
          'Replace the gateway URL, API key, and model with your own configuration.'
        )}
      </p>
    </div>
  )
}
