<template>
  <Teleport to="body">
    <div
      class="hf-overlay"
      role="dialog"
      aria-modal="true"
      @mousedown.self="close"
      @keydown.esc.window="close"
    >
      <div class="hf-dialog">
        <div class="hf-header">
          <span class="hf-title">{{ t('hostForwards.title') }}</span>
          <button class="hf-close" :title="t('common.close')" @click="close">✕</button>
        </div>

        <div class="hf-body">
          <!-- 新增表单 -->
          <div class="hf-form">
            <div class="hf-row">
              <label class="hf-field hf-kind-field">
                <span class="hf-label">{{ t('fwd.kind') }}</span>
                <select v-model="kind" class="hf-input">
                  <option value="local">{{ t('fwd.local') }}</option>
                  <option value="remote">{{ t('fwd.remote') }}</option>
                  <option value="dynamic">{{ t('fwd.dynamic') }}</option>
                </select>
              </label>
              <label class="hf-field">
                <span class="hf-label">{{ t('fwd.bind') }}</span>
                <input
                  v-model="bind"
                  class="hf-input"
                  type="text"
                  :placeholder="t('fwd.bindPlaceholder')"
                  spellcheck="false"
                  @keydown.enter="add"
                />
              </label>
              <label class="hf-field">
                <span class="hf-label">{{ t('fwd.target') }}</span>
                <input
                  v-model="target"
                  class="hf-input"
                  type="text"
                  :placeholder="t('fwd.targetPlaceholder')"
                  spellcheck="false"
                  :disabled="kind === 'dynamic'"
                  @keydown.enter="add"
                />
              </label>
            </div>
            <div class="hf-add-row">
              <button class="btn-primary" :disabled="busy" @click="add">{{ t('fwd.add') }}</button>
            </div>
          </div>

          <div v-if="formError" class="hf-error">{{ formError }}</div>

          <!-- 现有转发列表 -->
          <div class="hf-list-title">
            {{ t('hostForwards.listTitle') }}
            <span class="hf-list-hint">{{ t('hostForwards.hint') }}</span>
          </div>
          <div v-if="forwards.length === 0" class="hf-empty">{{ t('fwd.empty') }}</div>
          <div v-else class="hf-list">
            <div v-for="f in forwards" :key="f._k" class="hf-item">
              <span class="hf-item-kind" :class="'kind-' + f.kind">{{ f.kind }}</span>
              <span class="hf-item-spec">{{ f.bind }}<template v-if="f.target"> → {{ f.target }}</template></span>
              <button class="hf-item-del" :title="t('common.delete')" @click="remove(f._k)">✕</button>
            </div>
          </div>
        </div>

        <div class="hf-footer">
          <button class="btn-ghost" :disabled="busy" @click="close">{{ t('common.cancel') }}</button>
          <button class="btn-primary" :disabled="busy" @click="save">{{ t('common.save') }}</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { updateHost } from '../utils/api'
import { t } from '../utils/i18n'
import { logger } from '../utils/logger'
import type { Host, HostForward } from '../utils/types'

const props = defineProps<{
    host: Host
}>()

const emit = defineEmits<{
    (e: 'close'): void
    (e: 'saved'): void
}>()

// 本地行模型:HostForward + 临时 key(仅用于 v-for 与删除定位,保存时剔除)
interface ForwardRow extends HostForward {
    _k: number
}

let seq = 0

const forwards = ref<ForwardRow[]>(
    (props.host.forwards || []).map((f) => ({ ...f, _k: ++seq })),
)
const kind = ref<'local' | 'remote' | 'dynamic'>('local')
const bind = ref('')
const target = ref('')
const busy = ref(false)
const formError = ref('')

function add() {
    const b = bind.value.trim()
    const tgt = target.value.trim()
    if (!b || (kind.value !== 'dynamic' && !tgt)) {
        formError.value = t('fwd.required')
        return
    }
    forwards.value.push({
        _k: ++seq,
        kind: kind.value,
        bind: b,
        ...(kind.value === 'dynamic' ? {} : { target: tgt }),
    })
    bind.value = ''
    target.value = ''
    kind.value = 'local'
    formError.value = ''
}

function remove(k: number) {
    forwards.value = forwards.value.filter((f) => f._k !== k)
}

// 保存:整字段替换 PUT /api/hosts/{id}(与 HostFormModal 同一契约)
async function save() {
    if (busy.value) return
    busy.value = true
    formError.value = ''
    try {
        const clean: HostForward[] = forwards.value.map((f) => ({
            kind: f.kind,
            bind: f.bind,
            ...(f.target ? { target: f.target } : {}),
        }))
        await updateHost({ ...props.host, forwards: clean })
        logger.info('host', 'saved host forwards host=%s count=%d', props.host.id, clean.length)
        emit('saved')
    } catch (err) {
        logger.warn('host', 'failed to save forwards host=%s: %s', props.host.id, err)
        formError.value = err instanceof Error ? err.message : String(err) || t('hostForwards.saveFailed')
    } finally {
        busy.value = false
    }
}

function close() {
    emit('close')
}
</script>

<style scoped>
.hf-overlay {
    position: fixed;
    inset: 0;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--overlay);
}

.hf-dialog {
    width: 480px;
    max-width: calc(100vw - 32px);
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    max-height: calc(100vh - 48px);
}

.hf-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border-tab);
}

.hf-title {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--fg-bright);
}

.hf-close {
    background: none;
    border: none;
    color: var(--fg-dim);
    font-size: 13px;
    line-height: 1;
    padding: 3px 6px;
    border-radius: 3px;
    cursor: pointer;
}

.hf-close:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.hf-body {
    padding: 14px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    overflow-y: auto;
}

.hf-form {
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.hf-row {
    display: flex;
    gap: 8px;
}

.hf-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex: 1 1 0;
    min-width: 0;
}

.hf-kind-field {
    flex: 0 0 120px;
}

.hf-label {
    font-size: 12px;
    color: var(--fg-muted);
}

.hf-input {
    height: 28px;
    padding: 0 8px;
    background: var(--bg-input);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
    color: var(--fg);
    font-size: 12px;
    font-family: inherit;
    outline: none;
    width: 100%;
    min-width: 0;
}

.hf-input::placeholder {
    color: var(--fg-hint);
}

.hf-input:focus {
    border-color: var(--accent);
}

.hf-input:disabled {
    opacity: 0.5;
}

.hf-add-row {
    display: flex;
    justify-content: flex-end;
}

.btn-primary {
    height: 26px;
    padding: 0 14px;
    background: var(--accent);
    border: none;
    border-radius: 3px;
    color: var(--fg-bright);
    font-size: 12px;
    font-family: inherit;
    cursor: pointer;
}

.btn-primary:hover {
    filter: brightness(1.1);
}

.btn-primary:disabled {
    opacity: 0.6;
    cursor: default;
}

.btn-ghost {
    height: 26px;
    padding: 0 14px;
    background: none;
    border: 1px solid var(--border-tab);
    border-radius: 3px;
    color: var(--fg-dim);
    font-size: 12px;
    font-family: inherit;
    cursor: pointer;
}

.btn-ghost:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.btn-ghost:disabled {
    opacity: 0.6;
    cursor: default;
}

.hf-error {
    font-size: 12px;
    line-height: 1.5;
    color: var(--net-bad);
    word-break: break-word;
}

.hf-list-title {
    font-size: 12px;
    color: var(--fg-muted);
    padding-top: 4px;
    border-top: 1px solid var(--border-tab);
    display: flex;
    align-items: baseline;
    gap: 8px;
}

.hf-list-hint {
    font-size: 11px;
    color: var(--fg-hint);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.hf-empty {
    font-size: 12px;
    color: var(--fg-hint);
    padding: 8px 0;
}

.hf-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

.hf-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    background: var(--bg-tab);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
}

.hf-item-kind {
    flex: 0 0 auto;
    font-size: 10px;
    line-height: 1;
    padding: 2px 5px;
    border-radius: 3px;
    border: 1px solid var(--border-tab);
    color: var(--fg-muted);
    text-transform: uppercase;
}

.kind-local {
    color: #3fb950;
}

.kind-remote {
    color: #d29922;
}

.kind-dynamic {
    color: #58a6ff;
}

.hf-item-spec {
    flex: 1 1 auto;
    min-width: 0;
    font-family: 'SF Mono', Consolas, monospace;
    font-size: 12px;
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.hf-item-del {
    flex: 0 0 auto;
    background: none;
    border: none;
    color: var(--fg-dim);
    font-size: 11px;
    cursor: pointer;
    padding: 2px 5px;
    border-radius: 3px;
}

.hf-item-del:hover {
    background: var(--bg-tab-hover);
    color: var(--net-bad);
}

.hf-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 10px 14px;
    border-top: 1px solid var(--border-tab);
    flex: 0 0 auto;
}
</style>