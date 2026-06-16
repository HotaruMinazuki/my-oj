// markdown-it 插件：用 KaTeX 渲染 LaTeX 数学公式。
// 支持行内公式 $...$ 与块级公式 $$...$$。
// 解析逻辑改编自 markdown-it-katex (MIT, waylonflinn)，适配 markdown-it 14。
// KaTeX 随前端一起打包（含字体），运行时无需联网。
import katex from 'katex'

/* markdown-it 的内部 state/token 类型未在本项目声明，这里统一用 any 处理。 */
/* eslint-disable @typescript-eslint/no-explicit-any */

// 判断某个 `$` 能否作为开/闭定界符：
// 紧跟空白的不能作开始；前面是空白、或后面是数字（如金额 $5）的不能作结束。
function isValidInlineDelim(state: any, pos: number) {
  const prevChar = pos > 0 ? state.src.charCodeAt(pos - 1) : -1
  const nextChar = pos + 1 <= state.posMax ? state.src.charCodeAt(pos + 1) : -1
  const canOpen = !(nextChar === 0x20 /* space */ || nextChar === 0x09 /* tab */)
  const canClose = !(
    prevChar === 0x20 ||
    prevChar === 0x09 ||
    (nextChar >= 0x30 /* 0 */ && nextChar <= 0x39 /* 9 */)
  )
  return { canOpen, canClose }
}

function mathInline(state: any, silent: boolean): boolean {
  if (state.src[state.pos] !== '$') return false

  let res = isValidInlineDelim(state, state.pos)
  if (!res.canOpen) {
    if (!silent) state.pending += '$'
    state.pos += 1
    return true
  }

  // 找到匹配的闭合 `$`，跳过被反斜杠转义的 `\$`。
  const start = state.pos + 1
  let match = start
  while ((match = state.src.indexOf('$', match)) !== -1) {
    let pos = match - 1
    while (state.src[pos] === '\\') pos -= 1
    if ((match - pos) % 2 === 1) break // 偶数个反斜杠 → 未转义
    match += 1
  }

  if (match === -1) {
    if (!silent) state.pending += '$'
    state.pos = start
    return true
  }

  if (match - start === 0) {
    // `$$` 紧挨着 → 当作两个普通 `$`
    if (!silent) state.pending += '$$'
    state.pos = start + 1
    return true
  }

  res = isValidInlineDelim(state, match)
  if (!res.canClose) {
    if (!silent) state.pending += '$'
    state.pos = start
    return true
  }

  if (!silent) {
    const token = state.push('math_inline', 'math', 0)
    token.markup = '$'
    token.content = state.src.slice(start, match)
  }
  state.pos = match + 1
  return true
}

function mathBlock(state: any, start: number, end: number, silent: boolean): boolean {
  let pos = state.bMarks[start] + state.tShift[start]
  let max = state.eMarks[start]
  if (pos + 2 > max) return false
  if (state.src.slice(pos, pos + 2) !== '$$') return false

  pos += 2
  let firstLine = state.src.slice(pos, max)
  if (silent) return true

  let found = false
  if (firstLine.trim().slice(-2) === '$$') {
    firstLine = firstLine.trim().slice(0, -2)
    found = true
  }

  let next = start
  let lastLine = ''
  while (!found) {
    next += 1
    if (next >= end) break
    pos = state.bMarks[next] + state.tShift[next]
    max = state.eMarks[next]
    if (pos < max && state.tShift[next] < state.blkIndent) break
    if (state.src.slice(pos, max).trim().slice(-2) === '$$') {
      const lastPos = state.src.slice(0, max).lastIndexOf('$$')
      lastLine = state.src.slice(pos, lastPos)
      found = true
    }
  }

  state.line = next + 1
  const token = state.push('math_block', 'math', 0)
  token.block = true
  token.content =
    (firstLine && firstLine.trim() ? firstLine + '\n' : '') +
    state.getLines(start + 1, next, state.tShift[start], true) +
    (lastLine && lastLine.trim() ? lastLine : '')
  token.map = [start, state.line]
  token.markup = '$$'
  return true
}

function escapeHtml(s: string): string {
  return s.replace(
    /[&<>"]/g,
    (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c] as string,
  )
}

function renderKatex(content: string, displayMode: boolean): string {
  try {
    return katex.renderToString(content, {
      displayMode,
      throwOnError: false, // 语法错误时高亮显示而非整页报错
      output: 'htmlAndMathml',
    })
  } catch {
    // KaTeX 彻底失败时退回展示原始公式文本，避免渲染崩溃。
    const esc = escapeHtml(content)
    return displayMode
      ? `<pre class="katex-error">${esc}</pre>`
      : `<code class="katex-error">${esc}</code>`
  }
}

export default function markdownKatex(md: any): void {
  md.inline.ruler.after('escape', 'math_inline', mathInline)
  md.block.ruler.after('blockquote', 'math_block', mathBlock, {
    alt: ['paragraph', 'reference', 'blockquote', 'list'],
  })
  md.renderer.rules.math_inline = (tokens: any, idx: number) =>
    renderKatex(tokens[idx].content, false)
  md.renderer.rules.math_block = (tokens: any, idx: number) =>
    renderKatex(tokens[idx].content, true) + '\n'
}
