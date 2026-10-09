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

import { cn } from '@/lib/utils'

import { HeroButtons } from '../hero-buttons'

interface CTAProps {
  className?: string
  isAuthenticated?: boolean
}

export function CTA(props: CTAProps) {
  const { t } = useTranslation()

  return (
    <section
      className={cn('home-section home-cta home-inverted', props.className)}
      aria-labelledby='home-cta-title'
    >
      <div className='home-shell relative'>
        <p className='home-eyebrow mb-10'>03 / {t('Start building')}</p>
        <h2 id='home-cta-title' className='home-heading'>
          {t('Ready to simplify')}
          <br />
          <span className='home-heading-secondary'>
            {t('your AI integration?')}
          </span>
        </h2>
        <div className='mt-12 grid gap-8 md:grid-cols-2 md:items-end'>
          <p className='max-w-md text-base leading-8'>
            {t(
              'Deploy your own gateway and start routing requests through your configured upstream services.'
            )}
          </p>
          <div className='flex flex-wrap items-center gap-3 md:justify-end'>
            <HeroButtons isAuthenticated={!!props.isAuthenticated} />
          </div>
        </div>
      </div>
    </section>
  )
}
