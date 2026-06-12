// 文件方式提交:在浏览器端读取源码文件,复用既有的 JSON 提交接口
// (source_code 字段,后端 binding 上限 65536 字节,与此处校验保持一致)。

const EXT_LANG: Record<string, string> = {
  cpp: 'C++17',
  cc:  'C++17',
  cxx: 'C++17',
  c:   'C',
  py:  'Python3',
}

// 文件选择器的 accept 列表;.txt 允许但不推断语言(保留当前选择)。
export const SOURCE_ACCEPT = '.cpp,.cc,.cxx,.c,.py,.txt'

// 与后端 submitRequest 的 source_code max=65536 对齐。
export const MAX_SOURCE_BYTES = 64 * 1024

export interface SourceFileResult {
  code: string
  /** 由扩展名推断出的语言;无法推断时为 undefined */
  language?: string
  filename: string
}

export async function readSourceFile(file: File): Promise<SourceFileResult> {
  if (file.size === 0) {
    throw new Error('文件内容为空')
  }
  if (file.size > MAX_SOURCE_BYTES) {
    throw new Error(`文件过大(${Math.ceil(file.size / 1024)}KB),最大支持 ${MAX_SOURCE_BYTES / 1024}KB`)
  }
  const code = await file.text()
  if (!code.trim()) {
    throw new Error('文件内容为空')
  }
  const ext = file.name.split('.').pop()?.toLowerCase() ?? ''
  return { code, language: EXT_LANG[ext], filename: file.name }
}
