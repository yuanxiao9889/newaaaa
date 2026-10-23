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
import { Terminal } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

type CodeLanguage = 'curl' | 'python' | 'javascript'

export type CodeSamples = Record<CodeLanguage, string>

const languageLabels: Record<CodeLanguage, string> = {
  curl: 'cURL',
  python: 'Python',
  javascript: 'JavaScript',
}

export function CodeExample({
  samples,
  title,
}: {
  samples: CodeSamples
  title: string
}) {
  const { t } = useTranslation()
  const [language, setLanguage] = useState<CodeLanguage>('curl')

  return (
    <div className='border-border/70 bg-card overflow-hidden rounded-xl border shadow-sm'>
      <div className='border-border/70 bg-muted/25 flex flex-wrap items-center gap-3 border-b px-3 py-2.5'>
        <div className='text-muted-foreground flex items-center gap-2 text-xs font-medium'>
          <Terminal className='size-3.5' />
          {title}
        </div>
        <Tabs
          value={language}
          onValueChange={(value) => setLanguage(value as CodeLanguage)}
          className='ml-auto'
        >
          <TabsList className='h-7 p-0.5'>
            {(['curl', 'python', 'javascript'] as const).map((item) => (
              <TabsTrigger
                key={item}
                value={item}
                className='h-6 px-2 text-[11px]'
              >
                {languageLabels[item]}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <CopyButton
          value={samples[language]}
          aria-label={t('Copy code')}
          className='size-8'
          iconClassName='size-3.5'
        />
      </div>
      <pre className='max-h-[34rem] overflow-auto bg-[#0b1020] p-4 text-[12px] leading-6 text-slate-200 dark:bg-black/35'>
        <code>{samples[language]}</code>
      </pre>
    </div>
  )
}
