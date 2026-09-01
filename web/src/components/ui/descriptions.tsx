import { ProDescriptions, type ProDescriptionsProps, type ProDescriptionsItemProps } from '@ant-design/pro-components'
import type { ReactNode } from 'react'

export type { ProDescriptionsProps, ProDescriptionsItemProps }

export function DetailDescriptions<T extends Record<string, any>>({
  title,
  column = 3,
  bordered = false,
  ...props
}: ProDescriptionsProps<T> & { title?: ReactNode }) {
  return (
    <ProDescriptions<T>
      title={title}
      column={column}
      bordered={bordered}
      size="small"
      {...props}
    />
  )
}

export function DescriptionsItem(props: ProDescriptionsItemProps) {
  return <ProDescriptions.Item {...props} />
}
