// 名称校验工具单测：覆盖「名称 / 主机名」两条规则与实时净化函数。
// 纯逻辑，无需 DOM，验证前端与后端 pkg/validate 规则一致。
import { describe, it, expect } from 'vitest'
import {
  MAX_NAME_LEN,
  validateName,
  validateHostName,
  sanitizeName,
  sanitizeHostName,
} from '@/lib/validate'

describe('validateName', () => {
  it('空值返回「不能为空」', () => {
    expect(validateName('')).toContain('不能为空')
  })

  it('合法：字母/数字/横杠/下划线', () => {
    expect(validateName('my_script-1')).toBeNull()
    expect(validateName('Agent_01')).toBeNull()
  })

  it('非法：空格', () => {
    expect(validateName('my name')).not.toBeNull()
  })

  it('非法：点号（普通名称不允许 FQDN）', () => {
    expect(validateName('a.b')).not.toBeNull()
  })

  it('超长返回「过长」', () => {
    expect(validateName('a'.repeat(MAX_NAME_LEN + 1))).toContain('过长')
  })
})

describe('validateHostName', () => {
  it('合法：允许点号（兼容 FQDN）', () => {
    expect(validateHostName('host.example.com')).toBeNull()
    expect(validateHostName('db-1.local')).toBeNull()
  })

  it('非法：空格', () => {
    expect(validateHostName('my host')).not.toBeNull()
  })
})

describe('sanitize* 实时净化', () => {
  it('sanitizeName 剔除空格与特殊字符', () => {
    expect(sanitizeName('a b!@#c')).toBe('abc')
  })

  it('sanitizeHostName 保留点号', () => {
    expect(sanitizeHostName('host. example')).toBe('host.example')
  })

  it('净化结果不超过最大长度', () => {
    expect(sanitizeName('x'.repeat(200)).length).toBe(MAX_NAME_LEN)
  })
})
