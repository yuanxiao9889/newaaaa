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

import { HeroTerminalDemo } from '../hero-terminal-demo'

export function HowItWorks() {
  const { t } = useTranslation()
  const steps = [
    {
      num: '01',
      title: t('Configure'),
      description: t(
        'Add your API keys, set up channels and configure access permissions'
      ),
    },
    {
      num: '02',
      title: t('Connect'),
      description: t(
        'Connect through OpenAI, Claude, Gemini, and other compatible API routes'
      ),
    },
    {
      num: '03',
      title: t('Monitor'),
      description: t(
        'Track usage, costs and performance with real-time analytics'
      ),
    },
  ]
  return (
    <section
      id='integration'
      className='home-section home-integration'
      aria-labelledby='integration-title'
    >
      <div className='home-shell grid items-start gap-12 lg:grid-cols-2 lg:gap-20'>
        <div>
          <p className='home-eyebrow mb-6'>02 / {t('How It Works')}</p>
          <h2 id='integration-title' className='home-heading'>
            {t('Three steps to get started')}
          </h2>
          <ol className='home-steps mt-12 flex flex-col'>
            {steps.map((step) => (
              <li
                key={step.num}
                className='border-border flex items-start gap-5 border-t py-6'
              >
                <span
                  className='flex size-10 shrink-0 items-center justify-center border border-current font-mono text-xs'
                  aria-hidden='true'
                >
                  {step.num}
                </span>
                <div>
                  <h3 className='mb-2 font-serif text-xl'>{step.title}</h3>
                  <p className='text-muted-foreground max-w-sm text-sm leading-7'>
                    {step.description}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        </div>
        <div className='min-w-0'>
          <HeroTerminalDemo />
        </div>
      </div>
    </section>
  )
}
