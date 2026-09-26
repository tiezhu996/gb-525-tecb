<script setup lang="ts">
import { computed } from 'vue'
import { AlertTriangle, CheckCircle2, GitCompareArrows, RotateCcw } from 'lucide-vue-next'
import type { AssessmentInputDiff } from '@/types/assessment'

const props = defineProps<{ diff: AssessmentInputDiff; busy?: boolean }>()
const emit = defineEmits<{ recompute: []; openRun: [id: number] }>()

const profileTypeLabels = { version_changed: '版本变化', removed: '谱已删除' } as const
const stepTypeLabels = { added: '步骤新增', removed: '步骤删除', modified: '步骤修改' } as const
const edgeTypeLabels = { added: '边新增', removed: '边删除', version_changed: '版本变化' } as const
const declaredTypeLabels = { added: '新增声明', removed: '移除声明' } as const

function formatEnabled(value?: boolean): string {
  if (value === undefined) return '—'
  return value ? '启用' : '停用'
}
const edgeLabel = (edge: { from_step_code?: string; to_step_code?: string; edge_id: number }) => {
  if (edge.from_step_code && edge.to_step_code) return `${edge.from_step_code} → ${edge.to_step_code}`
  return `接触边 #${edge.edge_id}`
}
const stepProfileLabel = (code?: string, id?: number): string => {
  if (code) return `${code} (#${id ?? 0})`
  return id ? `谱 #${id}` : '—'
}
const changeCount = computed(() => props.diff.profile_changes.length + props.diff.route_step_changes.length + props.diff.contact_edge_changes.length + (props.diff.route_change?.declared_allergen_changes.length ?? 0))
</script>

<template>
  <section class="diff-panel">
    <header class="diff-head"><GitCompareArrows :size="16" /><strong>输入差异归因</strong><span class="diff-count">{{ changeCount }} 项</span></header>

    <el-alert v-if="diff.superseded_by_id" type="info" :closable="false" show-icon>
      <template #title>该评估已被新评估 <button class="link-button mono" @click="emit('openRun', diff.superseded_by_id!)">#{{ diff.superseded_by_id }}</button> 替代，记录保持只读。</template>
    </el-alert>
    <el-alert v-else-if="!diff.route_available" type="error" :closable="false" show-icon :title="diff.recompute_reason || '评估引用的工艺路线已不存在'" />
    <el-alert v-else-if="!diff.has_changes" type="success" :closable="false" show-icon>
      <template #title>快照与当前输入逐项一致，现有评估结果仍然可用，无需重算。</template>
    </el-alert>

    <div v-if="diff.has_changes" class="diff-groups">
      <div v-if="diff.profile_changes.length" class="diff-group">
        <h4>过敏原谱</h4>
        <el-table :data="diff.profile_changes" size="small" border>
          <el-table-column label="谱" min-width="150"><template #default="{ row }"><strong>{{ row.profile_code || `谱 #${row.profile_id}` }}</strong><div class="muted">{{ row.material_name }}</div></template></el-table-column>
          <el-table-column label="变化" width="90"><template #default="{ row }">{{ profileTypeLabels[row.change_type as keyof typeof profileTypeLabels] }}</template></el-table-column>
          <el-table-column label="快照版本" width="80" prop="snapshot_version" />
          <el-table-column label="当前版本" width="80"><template #default="{ row }">{{ row.current_version || '—' }}</template></el-table-column>
        </el-table>
      </div>

      <div v-if="diff.route_step_changes.length" class="diff-group">
        <h4>路线步骤</h4>
        <el-table :data="diff.route_step_changes" size="small" border>
          <el-table-column prop="step_code" label="步骤代码" width="110" />
          <el-table-column label="变化" width="90"><template #default="{ row }">{{ stepTypeLabels[row.change_type as keyof typeof stepTypeLabels] }}<el-tag v-if="row.reordered" size="small" effect="plain" class="step-tag">顺序</el-tag></template></el-table-column>
          <el-table-column label="引用谱（快照 → 当前）" min-width="200"><template #default="{ row }"><span class="mono">{{ stepProfileLabel(row.snapshot_profile_code, row.snapshot_profile_id) }}</span><span v-if="row.current_profile_id && row.current_profile_id !== row.snapshot_profile_id" class="arrow-sep"> → </span><span v-if="row.current_profile_id" class="mono">{{ stepProfileLabel(row.current_profile_code, row.current_profile_id) }}</span></template></el-table-column>
          <el-table-column label="位置（快照 → 当前）" width="130"><template #default="{ row }">{{ row.snapshot_order < 0 ? '—' : row.snapshot_order + 1 }} → {{ row.current_order < 0 ? '—' : row.current_order + 1 }}</template></el-table-column>
        </el-table>
      </div>

      <div v-if="diff.contact_edge_changes.length" class="diff-group">
        <h4>接触边</h4>
        <el-table :data="diff.contact_edge_changes" size="small" border>
          <el-table-column label="边" min-width="170"><template #default="{ row }">#{{ row.edge_id }} <span class="muted">{{ edgeLabel(row) }}</span></template></el-table-column>
          <el-table-column label="变化" width="90"><template #default="{ row }">{{ edgeTypeLabels[row.change_type as keyof typeof edgeTypeLabels] }}</template></el-table-column>
          <el-table-column label="版本（快照 → 当前）" width="130"><template #default="{ row }">{{ row.snapshot_version || '—' }} → {{ row.current_version || '—' }}</template></el-table-column>
          <el-table-column label="启用（快照 → 当前）" width="140"><template #default="{ row }">{{ formatEnabled(row.snapshot_enabled) }} → {{ formatEnabled(row.current_enabled) }}</template></el-table-column>
        </el-table>
      </div>

      <div v-if="diff.route_change && diff.route_change.declared_allergen_changes.length" class="diff-group">
        <h4>路线声明过敏原</h4>
        <el-table :data="diff.route_change.declared_allergen_changes" size="small" border>
          <el-table-column prop="allergen" label="过敏原" min-width="160" />
          <el-table-column label="变化" width="110"><template #default="{ row }"><el-tag :type="row.change_type === 'added' ? 'success' : 'info'" size="small">{{ declaredTypeLabels[row.change_type as keyof typeof declaredTypeLabels] }}</el-tag></template></el-table-column>
        </el-table>
      </div>
    </div>

    <footer v-if="diff.route_change && diff.route_change.version_changed" class="diff-meta">
      <span>路线版本：v{{ diff.route_change.snapshot_version }} → v{{ diff.route_change.current_version }}</span>
      <span>当前路线状态：{{ diff.route_status }}</span>
    </footer>

    <div class="diff-action">
      <template v-if="diff.superseded_by_id">
        <el-button :icon="RotateCcw" disabled>已重算过</el-button>
      </template>
      <template v-else-if="diff.recompute_available">
        <el-button type="primary" :icon="RotateCcw" :loading="busy" @click="emit('recompute')">按当前输入重算</el-button>
        <span class="diff-hint">重算将生成新的待复核评估，本记录保持只读并标记替代关系。</span>
      </template>
      <template v-else-if="diff.route_available">
        <el-button :icon="CheckCircle2" disabled>无需重算</el-button>
        <span v-if="diff.recompute_reason" class="diff-hint"><AlertTriangle :size="13" />{{ diff.recompute_reason }}</span>
      </template>
    </div>
  </section>
</template>

<style scoped>
.diff-panel { display: grid; gap: 12px; margin: 14px 0; padding: 12px; border: 1px solid var(--line); background: var(--surface); }
.diff-head { display: flex; align-items: center; gap: 8px; }
.diff-head strong { font-size: 13px; }
.diff-count { margin-left: auto; font-size: 11px; color: var(--muted); }
.diff-groups { display: grid; gap: 12px; }
.diff-group h4 { margin: 0 0 6px; font-size: 11px; color: var(--muted); letter-spacing: 0.04em; }
.step-tag { margin-left: 6px; }
.arrow-sep { color: var(--muted); margin: 0 4px; }
.diff-meta { display: flex; flex-wrap: wrap; gap: 14px; font-size: 11px; color: var(--muted); }
.diff-action { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding-top: 8px; border-top: 1px dashed var(--line); }
.diff-hint { display: inline-flex; align-items: center; gap: 5px; font-size: 11px; color: var(--muted); }
</style>
