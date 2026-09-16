import { vi } from 'vitest'

export function jsonRes(body: unknown, status = 200): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: () => Promise.resolve(body),
  } as unknown as Response
}

/** Stub global fetch. `handler` 收到的 url 是完整请求地址（含 `/api/v1`）。 */
export function stubFetch(
  handler: (url: string, init?: RequestInit) => Response | Promise<Response>,
) {
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    return handler(String(input), init)
  })
  vi.stubGlobal('fetch', fn)
  return fn
}
