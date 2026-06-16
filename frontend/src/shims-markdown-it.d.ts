declare module 'markdown-it' {
  interface MarkdownItOptions {
    html?: boolean
    linkify?: boolean
    breaks?: boolean
    typographer?: boolean
    [key: string]: unknown
  }
  interface MarkdownIt {
    render(src: string, env?: unknown): string
    renderInline(src: string, env?: unknown): string
    use(plugin: (md: MarkdownIt, ...params: unknown[]) => void, ...params: unknown[]): MarkdownIt
  }
  type MarkdownItCtor = {
    new (options?: MarkdownItOptions): MarkdownIt
    (options?: MarkdownItOptions): MarkdownIt
  }
  const md: MarkdownItCtor
  export default md
}
