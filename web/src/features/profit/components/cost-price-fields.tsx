import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

export type CostPriceForm = {
  channel_id: string
  model_name: string
  cost_mode: 'token' | 'request' | 'second'
  input_price: string
  output_price: string
  cache_read_price: string
  cache_creation_price: string
  cache_creation_1h_price: string
  image_input_price: string
  image_output_price: string
  audio_input_price: string
  audio_output_price: string
  request_price: string
  second_price: string
  effective_date: string
}

export function CostPriceFields(props: {
  form: CostPriceForm
  currency: string
  onChange: (patch: Partial<CostPriceForm>) => void
}) {
  const { t } = useTranslation()
  const groups: {
    title: string
    fields: { key: keyof CostPriceForm; label: string }[]
  }[] = [
    {
      title: t('Text tokens'),
      fields: [
        { key: 'input_price', label: t('Input') },
        { key: 'output_price', label: t('Output') },
      ],
    },
    {
      title: t('Prompt cache'),
      fields: [
        { key: 'cache_read_price', label: t('Cache Read') },
        { key: 'cache_creation_price', label: t('Cache Creation') },
        { key: 'cache_creation_1h_price', label: t('Cache Creation (1h)') },
      ],
    },
    {
      title: t('Image tokens'),
      fields: [
        { key: 'image_input_price', label: t('Image input') },
        { key: 'image_output_price', label: t('Image Output') },
      ],
    },
    {
      title: t('Audio tokens'),
      fields: [
        { key: 'audio_input_price', label: t('Audio Input') },
        { key: 'audio_output_price', label: t('Audio Output') },
      ],
    },
  ]

  return (
    <FieldGroup className='gap-4'>
      <Field>
        <FieldLabel id='profit-cost-mode'>{t('Cost billing mode')}</FieldLabel>
        <ToggleGroup
          aria-labelledby='profit-cost-mode'
          variant='outline'
          className='w-full flex-wrap'
          value={[props.form.cost_mode]}
          onValueChange={(values) => {
            const mode = values[0]
            if (mode === 'token' || mode === 'request' || mode === 'second') {
              props.onChange({ cost_mode: mode })
            }
          }}
        >
          <ToggleGroupItem className='flex-1' value='token'>
            {t('Token unit price')}
          </ToggleGroupItem>
          <ToggleGroupItem className='flex-1' value='request'>
            {t('Fixed per request')}
          </ToggleGroupItem>
          <ToggleGroupItem className='flex-1' value='second'>
            {t('Fixed per second')}
          </ToggleGroupItem>
        </ToggleGroup>
      </Field>
      {props.form.cost_mode === 'token' ? (
        <>
          <p className='text-muted-foreground text-xs'>
            {t('All token prices are in {{currency}} per 1M tokens.', {
              currency: props.currency,
            })}
          </p>
          <div className='grid items-start gap-4 sm:grid-cols-2'>
            {groups.map((group) => (
              <FieldSet
                key={group.title}
                className='bg-muted/20 min-w-0 rounded-lg border p-3'
              >
                <FieldLegend variant='label'>{group.title}</FieldLegend>
                <FieldGroup className='gap-3'>
                  {group.fields.map((field) => (
                    <Field key={field.key}>
                      <FieldLabel
                        htmlFor={`profit-${field.key}`}
                        className='text-muted-foreground text-xs'
                      >
                        {field.label}
                      </FieldLabel>
                      <Input
                        id={`profit-${field.key}`}
                        type='number'
                        min='0'
                        step='any'
                        placeholder='0'
                        value={props.form[field.key]}
                        onChange={(event) =>
                          props.onChange({ [field.key]: event.target.value })
                        }
                      />
                    </Field>
                  ))}
                </FieldGroup>
              </FieldSet>
            ))}
          </div>
        </>
      ) : (
        <Field>
          <FieldLabel htmlFor='profit-fixed-price'>
            {props.form.cost_mode === 'request'
              ? t('Fixed per request ({{currency}})', {
                  currency: props.currency,
                })
              : t('Fixed per second ({{currency}})', {
                  currency: props.currency,
                })}
          </FieldLabel>
          <Input
            id='profit-fixed-price'
            type='number'
            min='0'
            step='any'
            placeholder='0'
            value={
              props.form.cost_mode === 'request'
                ? props.form.request_price
                : props.form.second_price
            }
            onChange={(event) =>
              props.onChange({
                [props.form.cost_mode === 'request'
                  ? 'request_price'
                  : 'second_price']: event.target.value,
              })
            }
          />
          {props.form.cost_mode === 'second' && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'For video tasks, per-second cost uses the task duration from logs when available.'
              )}
            </p>
          )}
        </Field>
      )}
      <Field>
        <FieldLabel htmlFor='profit-effective-date'>
          {t('Effective from')}
        </FieldLabel>
        <Input
          id='profit-effective-date'
          type='date'
          required
          value={props.form.effective_date}
          onChange={(event) =>
            props.onChange({ effective_date: event.target.value })
          }
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Applies from the start of this date. Earlier usage keeps its previous cost price.'
          )}
        </p>
      </Field>
    </FieldGroup>
  )
}
