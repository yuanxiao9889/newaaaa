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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorStatus } from '@/lib/server-error-message'

import {
  bridgeManagementQueryKey,
  getBridgeManagement,
  saveBridgePolicy,
} from '../api'
import {
  createBridgePolicySchema,
  type BridgePolicyFormValues,
} from '../lib/policy-form'
import type { BridgeManagementData, PolicyRevision } from '../types'

export function usePolicyEditor(data: BridgeManagementData) {
  const { t } = useTranslation()
  const client = useQueryClient()
  // Checks may refresh the server snapshot, but never replace this editing base.
  const [baseVersion, setBaseVersion] = useState(data.version)
  const [conflict, setConflict] = useState(false)
  const [replacement, setReplacement] = useState<
    'reload' | PolicyRevision | null
  >(null)
  const form = useForm<BridgePolicyFormValues>({
    resolver: zodResolver(
      createBridgePolicySchema(data.fields, data.policy, t)
    ),
    defaultValues: { policy: data.policy, reason: '' },
  })
  const applySaved = (next: BridgeManagementData) => {
    client.setQueryData(bridgeManagementQueryKey, next)
    form.reset({ policy: next.policy, reason: '' })
    setBaseVersion(next.version)
    setConflict(false)
  }
  const save = useMutation({
    mutationFn: saveBridgePolicy,
    retry: false,
    onSuccess: (next) => {
      applySaved(next)
      toast.success(t('Bridge policy saved'))
    },
    onError: (error) => {
      if (getServerErrorStatus(error) === 409) setConflict(true)
      handleServerError(error)
    },
  })
  const reload = useMutation({
    mutationFn: getBridgeManagement,
    retry: false,
    onSuccess: applySaved,
    onError: (error) => handleServerError(error),
  })
  const replaceDraft = (next: 'reload' | PolicyRevision) => {
    if (next === 'reload') {
      reload.mutate()
    } else {
      form.setValue('policy', next.policy, {
        shouldDirty: true,
        shouldValidate: true,
      })
      form.setValue(
        'reason',
        t('Restore policy version {{version}}', { version: next.version }),
        { shouldDirty: true }
      )
    }
    setReplacement(null)
  }
  return {
    form,
    baseVersion,
    save,
    reload,
    replacement,
    setReplacement,
    replaceDraft,
    busy: save.isPending || reload.isPending,
    outdated: conflict || data.version !== baseVersion,
  }
}
