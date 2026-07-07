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
'use client'

import {
  CheckmarkCircle02Icon,
  InformationCircleIcon,
  Alert02Icon,
  MultiplicationSignCircleIcon,
  Loading03Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Toaster as Sonner, type ToasterProps } from 'sonner'

import { useTheme } from '@/context/theme-provider'
import { cn } from '@/lib/utils'

function Toaster({
  className,
  style,
  toastOptions,
  ...props
}: ToasterProps) {
  const { resolvedTheme } = useTheme()

  return (
    <Sonner
      {...props}
      theme={resolvedTheme}
      className={cn('toaster group [&_div[data-content]]:w-full', className)}
      icons={{
        success: (
          <HugeiconsIcon
            icon={CheckmarkCircle02Icon}
            strokeWidth={2}
            className='size-4'
          />
        ),
        info: (
          <HugeiconsIcon
            icon={InformationCircleIcon}
            strokeWidth={2}
            className='size-4'
          />
        ),
        warning: (
          <HugeiconsIcon
            icon={Alert02Icon}
            strokeWidth={2}
            className='size-4'
          />
        ),
        error: (
          <HugeiconsIcon
            icon={MultiplicationSignCircleIcon}
            strokeWidth={2}
            className='size-4'
          />
        ),
        loading: (
          <HugeiconsIcon
            icon={Loading03Icon}
            strokeWidth={2}
            className='size-4 animate-spin'
          />
        ),
      }}
      style={
        {
          ...style,
          '--normal-bg': 'var(--popover)',
          '--normal-text': 'var(--popover-foreground)',
          '--normal-border': 'var(--border)',
          '--success-bg':
            'color-mix(in oklch, var(--success) 16%, var(--popover))',
          '--success-border':
            'color-mix(in oklch, var(--success) 35%, var(--border))',
          '--success-text': 'var(--success)',
          '--info-bg': 'color-mix(in oklch, var(--info) 16%, var(--popover))',
          '--info-border':
            'color-mix(in oklch, var(--info) 35%, var(--border))',
          '--info-text': 'var(--info)',
          '--warning-bg':
            'color-mix(in oklch, var(--warning) 18%, var(--popover))',
          '--warning-border':
            'color-mix(in oklch, var(--warning) 38%, var(--border))',
          '--warning-text': 'var(--warning)',
          '--error-bg':
            'color-mix(in oklch, var(--destructive) 16%, var(--popover))',
          '--error-border':
            'color-mix(in oklch, var(--destructive) 35%, var(--border))',
          '--error-text': 'var(--destructive)',
          '--border-radius': 'var(--radius)',
        } as React.CSSProperties
      }
      toastOptions={{
        ...toastOptions,
        classNames: {
          toast:
            'group toast group-[.toaster]:bg-popover group-[.toaster]:text-popover-foreground group-[.toaster]:border-border group-[.toaster]:shadow-lg',
          title: 'group-[.toast]:text-popover-foreground',
          description: 'group-[.toast]:text-muted-foreground',
          closeButton:
            'group-[.toast]:bg-popover group-[.toast]:text-popover-foreground group-[.toast]:border-border',
          actionButton:
            'group-[.toast]:bg-primary group-[.toast]:text-primary-foreground',
          cancelButton:
            'group-[.toast]:bg-muted group-[.toast]:text-muted-foreground',
          ...toastOptions?.classNames,
        },
      }}
    />
  )
}

export { Toaster }
