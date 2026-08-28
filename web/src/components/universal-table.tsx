
import { useState, useMemo, type ReactNode } from 'react'
import { Table as AntTable, Input, type TableProps } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { SearchOutlined } from '@ant-design/icons'
import { cn } from '@/lib/utils'

export interface FieldDef<T> {
  key: string
  title: ReactNode
  dataIndex?: keyof T | string
  width?: number | string
  sortable?: boolean
  render?: (value: any, record: T, index: number) => ReactNode
  className?: string
  align?: 'left' | 'center' | 'right'
}

export interface UniversalTableProps<T> {
  data: T[]
  fields: FieldDef<T>[]
  rowKey?: string | ((record: T) => string)
  pageSize?: number
  searchPlaceholder?: string
  searchKeys?: (keyof T)[]
  loading?: boolean
  emptyText?: ReactNode
  className?: string
  onRowClick?: (record: T) => void
  pagination?: boolean
  scroll?: TableProps<T>['scroll']
}

function SearchInput({ placeholder, onSearch }: { placeholder: string; onSearch: (q: string) => void }) {
  const [value, setValue] = useState('')
  return (
    <div className="relative w-full max-w-xs">
      <Input
        prefix={<SearchOutlined className="text-muted-foreground" />}
        placeholder={placeholder}
        value={value}
        onChange={(e) => {
          const v = e.target.value
          setValue(v)
          onSearch(v)
        }}
        allowClear
        className="pr-3"
      />
    </div>
  )
}

export function UniversalTable<T extends Record<string, any>>({
  data,
  fields,
  rowKey = 'id',
  pageSize = 20,
  searchPlaceholder = '搜索...',
  searchKeys,
  loading = false,
  emptyText = '暂无数据',
  className,
  onRowClick,
  pagination = true,
  scroll,
}: UniversalTableProps<T>) {
  const [searchQ, setSearchQ] = useState('')
  const [sortField, setSortField] = useState<string | null>(null)
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc' | null>(null)

  const columns = useMemo(() => {
    return fields.map((field) => ({
      title: field.title,
      dataIndex: field.dataIndex ?? field.key,
      key: field.key,
      width: field.width,
      align: field.align,
      className: field.className,
      sorter: field.sortable ? true : undefined,
      render: field.render,
    })) as ColumnsType<T>
  }, [fields])

  const filtered = useMemo(() => {
    if (!searchQ || !searchKeys || searchKeys.length === 0) return data
    return data.filter((item) =>
      searchKeys.some((k) => String(item[k] ?? '').toLowerCase().includes(searchQ.toLowerCase()))
    )
  }, [data, searchQ, searchKeys])

  const sorted = useMemo(() => {
    if (!sortField || !sortOrder) return filtered
    return [...filtered].sort((a, b) => {
      const av = a[sortField]
      const bv = b[sortField]
      if (av == null) return -1
      if (bv == null) return 1
      const cmp = String(av).localeCompare(String(bv))
      return sortOrder === 'asc' ? cmp : -cmp
    })
  }, [filtered, sortField, sortOrder])

  const tableColumns = useMemo(() => {
    return columns.map((col: any) => {
      const field = fields.find((f) => f.key === col.key)
      if (field?.sortable) {
        return {
          ...col,
          onSort: (_: any, __: any, currentSort: string) => {
            if (sortField === col.key) {
              setSortOrder(sortOrder === 'asc' ? 'desc' : sortOrder === 'desc' ? null : 'asc')
              if (sortOrder === 'desc') {
                setSortField(null)
                setSortOrder(null)
              } else {
                setSortOrder(sortOrder === 'asc' ? 'desc' : 'asc')
              }
            } else {
              setSortField(col.key!)
              setSortOrder('asc')
            }
          },
        }
      }
      return col
    })
  }, [columns, fields, sortField, sortOrder])

  return (
    <div className={cn('space-y-3', className)}>
      {searchKeys && searchKeys.length > 0 && (
        <SearchInput placeholder={searchPlaceholder} onSearch={setSearchQ} />
      )}
      <AntTable<T>
        columns={tableColumns}
        dataSource={sorted}
        rowKey={rowKey}
        size="middle"
        scroll={scroll}
        loading={loading}
        locale={{ emptyText }}
        pagination={pagination ? { pageSize, showSizeChanger: false } : false}
        onRow={onRowClick ? (record: T) => ({ onClick: () => onRowClick(record) }) : undefined}
      />
    </div>
  )
}
