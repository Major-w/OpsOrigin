// 会话导出（共享）: /ai 全页、浮窗助手、纯净模式三入口共用同一份导出逻辑。
// 抽到这里是为了杜绝三份拷贝 —— 此前只有 /ai 全页有导出，浮窗/纯净模式各抄一份的做法
// 已经在其他功能上付过维护代价（见 chat-kernel.tsx 头注释：三套重复 UI 的教训）。

export interface ExportableChatMessage {
  /** welcome 消息 id 为 '0'，导出时剔除 */
  id?: string
  role: 'user' | 'assistant'
  content: string
  errorText?: string
  attachments?: { name: string }[]
  toolCalls?: { name?: string; title?: string }[]
  citations?: string[]
}

/**
 * 将当前会话导出为 Markdown 文件（浏览器下载）。
 * 失败消息标注原因；附件仅记名；工具调用与引用以摘要行保留，保证导出可溯源。
 * @returns 是否实际导出（false = 没有可导出的消息，调用方据此给提示）
 */
export function exportSessionMarkdown(messages: ExportableChatMessage[]): boolean {
  const body = messages.filter(m => m.id !== '0' && (m.content || m.errorText))
  if (!body.length) return false
  const lines: string[] = ['# AI 会话导出', '', `- 导出时间：${new Date().toLocaleString()}`, `- 消息数：${body.length}`, '']
  for (const m of body) {
    const who = m.role === 'user' ? '🙋 用户' : '🤖 AI'
    const content = m.content || (m.errorText ? `（失败：${m.errorText}）` : '（无内容）')
    lines.push(`## ${who}`, '', content, '')
    if (m.attachments?.length) lines.push(`> 附件：${m.attachments.map(a => a.name).join('、')}`, '')
    if (m.toolCalls?.length) {
      const names = m.toolCalls.map(t => t.title || t.name).filter(Boolean).join('、')
      if (names) lines.push(`> 工具调用：${names}`, '')
    }
    if (m.citations?.length) lines.push(`> 引用：${m.citations.join('、')}`, '')
  }
  const blob = new Blob([lines.join('\n')], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `ai-session-${new Date().toISOString().slice(0, 10)}.md`
  a.click()
  URL.revokeObjectURL(url)
  return true
}
