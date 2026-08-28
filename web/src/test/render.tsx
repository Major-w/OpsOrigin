// 组件测试渲染骨架：统一包裹 Provider，后续组件测试直接 import { render }。
// 当前 OpsOrigin 前端未使用全局 Provider（zustand store 各自独立），故 wrapper 暂为透传；
// 若后续引入 Theme/Query/Router Provider，在此处集中注入即可。
import { type ReactElement, type ReactNode } from 'react'
import { render, type RenderOptions } from '@testing-library/react'

const AllProviders = ({ children }: { children: ReactNode }) => <>{children}</>

const customRender = (
  ui: ReactElement,
  options?: Omit<RenderOptions, 'wrapper'>,
) => render(ui, { wrapper: AllProviders, ...options })

export * from '@testing-library/react'
export { customRender as render }
