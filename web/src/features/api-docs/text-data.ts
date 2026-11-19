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
export const textExamples = [
  {
    title: 'OpenAI-compatible chat',
    path: '/v1/chat/completions',
    body: {
      model: 'gpt-5.6-sol',
      messages: [
        { role: 'user', content: 'Write a short scene set at sunrise.' },
      ],
      stream: false,
    },
  },
  {
    title: 'OpenAI Responses',
    path: '/v1/responses',
    body: {
      model: 'gpt-6.1-sol',
      input: 'Write a short scene set at sunrise.',
      stream: false,
    },
  },
  {
    title: 'Anthropic Messages',
    path: '/v1/messages',
    body: {
      model: 'claude-sonnet-4-6',
      max_tokens: 1024,
      messages: [
        { role: 'user', content: 'Write a short scene set at sunrise.' },
      ],
    },
  },
  {
    title: 'Gemini generateContent',
    path: '/v1beta/models/gemini-4-argon-ultra:generateContent',
    body: {
      contents: [
        {
          role: 'user',
          parts: [{ text: 'Write a short scene set at sunrise.' }],
        },
      ],
    },
  },
]
