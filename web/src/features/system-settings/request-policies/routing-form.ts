import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseHttpStatusCodeRules } from '@/lib/http-status-code-rules'

export function createRoutingPolicySchema(t: TFunction) {
  return z.object({
    ImageInputRetentionMinutes: z.number().refine(value => [10, 20, 30].includes(value)),
    RetryCycleEnabled: z.boolean(),
    RetryTimeBudgetEnabled: z.boolean(),
    RetryTimeBudgetSeconds: z.number().int().min(1).max(3600),
    RetryTimes: z.number().int().min(0).max(99),
    AutomaticRetryStatusCodes: z
      .string()
      .refine(
        (value) => parseHttpStatusCodeRules(value).ok,
        t('Invalid status code rules')
      ),
    channel_affinity_setting: z.object({
      enabled: z.boolean(),
      session_mode: z.enum(['', 'off', 'prefer', 'strict']),
      switch_on_success: z.boolean(),
      keep_on_channel_disabled: z.boolean(),
      max_entries: z.number().int().min(0),
      default_ttl_seconds: z.number().int().min(0),
      rules: z.string().superRefine((value, context) => {
        try {
          const rules: unknown = JSON.parse(value)
          if (!Array.isArray(rules)) {
            context.addIssue({
              code: 'custom',
              message: t('Rules JSON must be an array'),
            })
          }
        } catch {
          context.addIssue({
            code: 'custom',
            message: t('Invalid rules JSON format'),
          })
        }
      }),
    }),
  })
}

export type RoutingPolicyFormValues = z.infer<
  ReturnType<typeof createRoutingPolicySchema>
>

export function routingPolicyFormValues(
  options: Record<string, string>
): RoutingPolicyFormValues {
  return {
    ImageInputRetentionMinutes: Number(options.ImageInputRetentionMinutes ?? 20),
    RetryCycleEnabled: options.RetryCycleEnabled === 'true',
    RetryTimeBudgetEnabled: options.RetryTimeBudgetEnabled === 'true',
    RetryTimeBudgetSeconds: Number(options.RetryTimeBudgetSeconds ?? 60),
    RetryTimes: Number(options.RetryTimes),
    AutomaticRetryStatusCodes: options.AutomaticRetryStatusCodes,
    channel_affinity_setting: {
      enabled: options['channel_affinity_setting.enabled'] === 'true',
      session_mode: (options['channel_affinity_setting.session_mode'] ||
        '') as RoutingPolicyFormValues['channel_affinity_setting']['session_mode'],
      switch_on_success:
        options['channel_affinity_setting.switch_on_success'] === 'true',
      keep_on_channel_disabled:
        options['channel_affinity_setting.keep_on_channel_disabled'] === 'true',
      max_entries: Number(options['channel_affinity_setting.max_entries']),
      default_ttl_seconds: Number(
        options['channel_affinity_setting.default_ttl_seconds']
      ),
      rules: options['channel_affinity_setting.rules'] || '[]',
    },
  }
}

export function routingPolicyOptions(
  values: RoutingPolicyFormValues
): Record<string, string> {
  return {
    ImageInputRetentionMinutes: String(values.ImageInputRetentionMinutes),
    RetryCycleEnabled: String(values.RetryCycleEnabled),
    RetryTimeBudgetEnabled: String(values.RetryTimeBudgetEnabled),
    RetryTimeBudgetSeconds: String(values.RetryTimeBudgetSeconds),
    RetryTimes: String(values.RetryTimes),
    AutomaticRetryStatusCodes: values.AutomaticRetryStatusCodes,
    ...Object.fromEntries(
      Object.entries(values.channel_affinity_setting).map(([key, value]) => [
        `channel_affinity_setting.${key}`,
        String(value),
      ])
    ),
  }
}
