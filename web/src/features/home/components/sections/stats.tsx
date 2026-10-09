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

const PROVIDERS = [
  'OpenAI',
  'Anthropic',
  'Google Gemini',
  'DeepSeek',
  'Qwen',
  'Meta Llama',
]

export function Stats() {
  const { t } = useTranslation()
  return (
    <section
      className='home-providers home-inverted'
      aria-label={t('Multi-protocol Compatible')}
    >
      <div className='home-shell relative py-10 md:py-14'>
        <p className='home-eyebrow mb-8 text-center'>
          {t('Multi-protocol Compatible')}
        </p>
        <ul className='grid grid-cols-2 items-center gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-6'>
          {PROVIDERS.map((provider) => (
            <li
              key={provider}
              className='text-center font-serif text-lg tracking-tight md:text-xl'
            >
              {provider}
            </li>
          ))}
        </ul>
      </div>
    </section>
  )
}
