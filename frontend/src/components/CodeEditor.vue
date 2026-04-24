<template>
  <div class="code-editor-wrap">
    <div v-if="showToolbar" class="code-editor-toolbar">
      <span class="ce-label">
        <el-icon><EditPen /></el-icon>
        <span>{{ props.language }}</span>
      </span>
      <span class="ce-spacer" />
      <span v-if="draftSavedAt" class="ce-draft-hint">
        <el-icon><CircleCheck /></el-icon>
        草稿已保存 {{ draftSavedLabel }}
      </span>
      <el-tooltip content="减小字号" placement="top">
        <el-button link size="small" :icon="Minus" @click="changeFont(-1)" />
      </el-tooltip>
      <span class="ce-font-size">{{ fontSize }}px</span>
      <el-tooltip content="增大字号" placement="top">
        <el-button link size="small" :icon="Plus" @click="changeFont(1)" />
      </el-tooltip>
      <el-tooltip :content="theme === 'vs-dark' ? '切换浅色' : '切换深色'" placement="top">
        <el-button link size="small" :icon="theme === 'vs-dark' ? Sunny : Moon" @click="toggleTheme" />
      </el-tooltip>
      <el-tooltip content="清空草稿" placement="top">
        <el-button link size="small" :icon="Delete" @click="clearDraft" />
      </el-tooltip>
    </div>
    <div ref="editorEl" class="code-editor" />
    <div v-if="showHint" class="code-editor-hint">
      <kbd>Ctrl</kbd> + <kbd>Enter</kbd> 提交 · <kbd>Ctrl</kbd> + <kbd>S</kbd> 保存草稿
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount, computed } from 'vue'
import loader from '@monaco-editor/loader'
import type * as Monaco from 'monaco-editor'
import {
  EditPen, CircleCheck, Plus, Minus, Sunny, Moon, Delete,
} from '@element-plus/icons-vue'

const props = withDefaults(defineProps<{
  modelValue: string
  language?: string
  readOnly?: boolean
  height?: string
  /** Persist draft to localStorage under this key (disables when undefined). */
  draftKey?: string
  /** Show header toolbar. */
  showToolbar?: boolean
  /** Show hint strip under editor. */
  showHint?: boolean
}>(), {
  language: 'cpp',
  readOnly: false,
  height: '480px',
  draftKey: undefined,
  showToolbar: true,
  showHint: true,
})

const emit = defineEmits<{
  'update:modelValue': [v: string]
  'submit': []
}>()

const editorEl = ref<HTMLElement>()
let editor: Monaco.editor.IStandaloneCodeEditor | null = null
let monaco: typeof Monaco | null = null

// Map OJ language names → Monaco language IDs
const LANG_MAP: Record<string, string> = {
  'C': 'c', 'C++17': 'cpp', 'C++20': 'cpp',
  'Java21': 'java', 'Python3': 'python',
  'Go': 'go', 'Rust': 'rust',
}

// ─── Persisted UI prefs ───────────────────────────────────────────────────
const fontSize = ref<number>(Number(localStorage.getItem('oj_editor_fs')) || 14)
const theme    = ref<'vs-dark' | 'vs'>((localStorage.getItem('oj_editor_theme') as 'vs-dark' | 'vs') || 'vs-dark')

function changeFont(delta: number) {
  fontSize.value = Math.min(24, Math.max(10, fontSize.value + delta))
  localStorage.setItem('oj_editor_fs', String(fontSize.value))
  editor?.updateOptions({ fontSize: fontSize.value })
}
function toggleTheme() {
  theme.value = theme.value === 'vs-dark' ? 'vs' : 'vs-dark'
  localStorage.setItem('oj_editor_theme', theme.value)
  monaco?.editor.setTheme(theme.value)
}

// ─── Draft autosave ───────────────────────────────────────────────────────
const DRAFT_NS = 'oj_draft:'
const draftSavedAt = ref<number | null>(null)
const draftSavedLabel = computed(() => {
  if (!draftSavedAt.value) return ''
  const diff = Math.floor((Date.now() - draftSavedAt.value) / 1000)
  if (diff < 5) return '刚刚'
  if (diff < 60) return `${diff}秒前`
  return `${Math.floor(diff / 60)}分钟前`
})

let saveTimer: ReturnType<typeof setTimeout> | null = null
function scheduleDraftSave(val: string) {
  if (!props.draftKey) return
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = setTimeout(() => {
    localStorage.setItem(DRAFT_NS + props.draftKey, val)
    draftSavedAt.value = Date.now()
  }, 600)
}
function loadDraft(): string | null {
  if (!props.draftKey) return null
  return localStorage.getItem(DRAFT_NS + props.draftKey)
}
function clearDraft() {
  if (!props.draftKey) return
  localStorage.removeItem(DRAFT_NS + props.draftKey)
  draftSavedAt.value = null
  editor?.setValue('')
  emit('update:modelValue', '')
}

// Tick label every 10s so "N秒前" refreshes without timer flood
let labelTimer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
  loader.config({ paths: { vs: 'https://cdn.jsdelivr.net/npm/monaco-editor@0.47.0/min/vs' } })
  monaco = await loader.init()

  // Priority: existing modelValue > saved draft
  const initial = props.modelValue || loadDraft() || ''
  if (initial && initial !== props.modelValue) {
    emit('update:modelValue', initial)
  }

  editor = monaco.editor.create(editorEl.value!, {
    value: initial,
    language: LANG_MAP[props.language] ?? props.language,
    theme: theme.value,
    readOnly: props.readOnly,
    minimap: { enabled: false },
    fontSize: fontSize.value,
    lineNumbers: 'on',
    scrollBeyondLastLine: false,
    automaticLayout: true,
    tabSize: 4,
    renderLineHighlight: 'line',
    smoothScrolling: true,
  })

  editor.onDidChangeModelContent(() => {
    const v = editor!.getValue()
    emit('update:modelValue', v)
    scheduleDraftSave(v)
  })

  // Ctrl/Cmd + Enter → submit; Ctrl/Cmd + S → force save draft
  editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => {
    if (!props.readOnly) emit('submit')
  })
  editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
    if (saveTimer) clearTimeout(saveTimer)
    if (props.draftKey) {
      localStorage.setItem(DRAFT_NS + props.draftKey, editor!.getValue())
      draftSavedAt.value = Date.now()
    }
  })

  labelTimer = setInterval(() => {
    // trigger reactivity refresh for relative time label
    if (draftSavedAt.value) draftSavedAt.value = draftSavedAt.value
  }, 10_000)
})

watch(() => props.language, lang => {
  if (editor && monaco) {
    const monacoLang = LANG_MAP[lang] ?? lang
    monaco.editor.setModelLanguage(editor.getModel()!, monacoLang)
  }
})

watch(() => props.modelValue, val => {
  if (editor && editor.getValue() !== val) {
    editor.setValue(val)
  }
})

// If draftKey changes (e.g., language switch for same problem), try loading new draft
watch(() => props.draftKey, () => {
  if (!editor) return
  const d = loadDraft()
  if (d !== null && d !== editor.getValue()) {
    editor.setValue(d)
    emit('update:modelValue', d)
  }
})

onBeforeUnmount(() => {
  if (saveTimer) clearTimeout(saveTimer)
  if (labelTimer) clearInterval(labelTimer)
  editor?.dispose()
})

defineExpose({ clearDraft, focus: () => editor?.focus() })
</script>

<style scoped>
.code-editor-wrap {
  border: 1px solid var(--oj-border);
  border-radius: var(--oj-radius);
  overflow: hidden;
  background: #1e1e1e;
}
.code-editor-toolbar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  background: #2d2d2d;
  border-bottom: 1px solid #1a1a1a;
  color: #ccc;
  font-size: 12px;
}
.ce-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-weight: 500;
}
.ce-spacer { flex: 1; }
.ce-draft-hint {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: #67c23a;
  margin-right: 8px;
}
.ce-font-size {
  min-width: 28px;
  text-align: center;
  color: #aaa;
}
.code-editor-toolbar :deep(.el-button) {
  color: #ccc;
}
.code-editor-toolbar :deep(.el-button:hover) {
  color: var(--oj-primary);
}
.code-editor {
  width: 100%;
  height: v-bind(height);
}
.code-editor-hint {
  padding: 6px 10px;
  background: #f5f7fa;
  border-top: 1px solid var(--oj-border);
  font-size: 12px;
  color: var(--oj-text-3);
}
.code-editor-hint kbd {
  display: inline-block;
  padding: 1px 6px;
  margin: 0 2px;
  background: #fff;
  border: 1px solid var(--oj-border);
  border-bottom-width: 2px;
  border-radius: 3px;
  font-family: ui-monospace, monospace;
  font-size: 11px;
  color: var(--oj-text-2);
}
</style>
