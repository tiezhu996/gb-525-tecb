<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Check, Clock3, Eye, Play, Plus, RefreshCw, RotateCcw, X } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'
import { assessmentApi, routeApi } from '@/api/domain'
import EvidencePathPanel from '@/components/common/EvidencePathPanel.vue'
import RiskBadge from '@/components/common/RiskBadge.vue'
import { useAssessmentPolling } from '@/hooks/useAssessmentPolling'
import { useAuth } from '@/hooks/useAuth'
import { useAssessmentStore } from '@/stores/assessments'
import { assessmentLabels, type AssessmentRun, type AssessmentStatus, type StaleDiff, type StaleDiffItem } from '@/types/assessment'
import type { ProcessRoute } from '@/types/domain'
import { dateTime } from '@/utils/format'

const auth = useAuth()
const assessmentStore = useAssessmentStore()
const routes = ref<ProcessRoute[]>([])
const runs = computed(() => assessmentStore.runs)
const loading = computed(() => assessmentStore.loading)
const workingId = ref<number>()
const createDialog = ref(false)
const routeId = ref<number>()
const detailOpen = ref(false)
const selected = ref<AssessmentRun>()
const selectedRiskIndex = ref(0)
const reviewOpen = ref(false)
const reviewDecision = ref<'accepted' | 'rejected'>('accepted')
const reviewReason = ref('')
const staleDiff = ref<StaleDiff>()
const diffLoading = ref(false)
const routesById = computed(() => Object.fromEntries(routes.value.map((route) => [route.id, route])))
const hasCalculating = () => runs.value.some((run) => run.assessment_status === 'calculating')
const polling = useAssessmentPolling(load, hasCalculating)
const statusLabel = (status: AssessmentStatus) => assessmentLabels[status]
const diffKindLabels: Record<StaleDiffItem['kind'], string> = { route: '路线', profile: '过敏原谱', contact_edge: '接触边' }
const diffChangeLabels: Record<StaleDiffItem['change'], string> = { steps_changed: '步骤变化', declared_changed: '声明变化', version_changed: '版本变化', added: '新增', removed: '移除' }
const canRecalculate = computed(() => {
  const run = selected.value
  if (!run || run.superseded_by_id) return false
  if (run.assessment_status === 'rejected') return true
  return run.assessment_status === 'stale' && !!staleDiff.value && staleDiff.value.items.length > 0
})

async function load() { await assessmentStore.load(); if (selected.value) selected.value = runs.value.find((run) => run.id === selected.value?.id) }
async function loadRoutes() { routes.value = (await routeApi.list({ page_size: 100 })).items; routeId.value = routes.value.find((route) => route.route_status === 'active')?.id }
async function createRun() { if (!routeId.value) return; const run = await assessmentStore.create(routeId.value); ElMessage.success(`评估 #${run.id} 已进入队列`); createDialog.value = false }
async function runOne(run: AssessmentRun) { workingId.value = run.id; polling.start(); try { await assessmentStore.execute(run.id); ElMessage.success(`评估 #${run.id} 已完成计算`) } finally { workingId.value = undefined; if (!hasCalculating()) polling.stop() } }
async function inspect(run: AssessmentRun) {
  selected.value = run; selectedRiskIndex.value = 0; detailOpen.value = true; staleDiff.value = undefined
  if (run.assessment_status === 'stale') { diffLoading.value = true; try { staleDiff.value = await assessmentApi.diff(run.id) } finally { diffLoading.value = false } }
}
function inspectById(id?: number) { const run = runs.value.find((item) => item.id === id); if (run) inspect(run) }
async function recalculate() {
  if (!selected.value) return
  workingId.value = selected.value.id
  try { const run = await assessmentStore.recalculate(selected.value.id); ElMessage.success(`已生成新评估 #${run.id}，回到待复核`); selected.value = runs.value.find((item) => item.id === selected.value?.id); staleDiff.value = undefined } finally { workingId.value = undefined }
}
function openReview(run: AssessmentRun, decision: 'accepted' | 'rejected') { selected.value = run; reviewDecision.value = decision; reviewReason.value = ''; reviewOpen.value = true }
async function review() { if (!selected.value || reviewReason.value.trim().length < 4) return; workingId.value = selected.value.id; try { await assessmentStore.review(selected.value.id, reviewDecision.value, reviewReason.value); ElMessage.success(reviewDecision.value === 'accepted' ? '评估已接受' : '评估已拒绝'); reviewOpen.value = false } finally { workingId.value = undefined } }
onMounted(async () => { await Promise.all([loadRoutes(), load()]) })
</script>

<template>
  <header class="page-header"><div class="page-header-copy"><h1>评估工作台</h1><p>不可变结果、版本快照与人工复核</p></div><div class="page-header-actions"><el-button v-if="auth.canEdit.value" type="primary" :icon="Plus" @click="createDialog = true">提交评估</el-button></div></header>
  <section class="content-band stack">
    <div class="metric-strip"><div class="metric"><span>评估总数</span><strong>{{ runs.length }}</strong></div><div class="metric"><span>待计算</span><strong>{{ runs.filter((item) => item.assessment_status === 'queued').length }}</strong></div><div class="metric"><span>待复核</span><strong>{{ runs.filter((item) => item.assessment_status === 'pending_review').length }}</strong></div><div class="metric"><span>过期结果</span><strong>{{ runs.filter((item) => item.assessment_status === 'stale').length }}</strong></div></div>
    <div class="data-surface">
      <div class="surface-header"><Clock3 :size="18" /><h2>评估记录</h2><span v-if="polling.polling.value">状态同步中</span><el-tooltip content="刷新"><el-button class="toolbar-spacer" :icon="RefreshCw" text circle aria-label="刷新" @click="load" /></el-tooltip></div>
      <el-table v-loading="loading" :data="runs" row-key="id">
        <el-table-column label="编号" width="80"><template #default="{ row }"><button class="link-button mono" @click="inspect(row)">#{{ row.id }}</button></template></el-table-column>
        <el-table-column label="路线" min-width="180"><template #default="{ row }"><strong>{{ routesById[row.route_id]?.route_code || `路线 #${row.route_id}` }}</strong><div class="muted row-sub">{{ routesById[row.route_id]?.product_name }}</div></template></el-table-column>
        <el-table-column label="状态" width="125"><template #default="{ row }"><span class="status-pill" :class="row.assessment_status">{{ statusLabel(row.assessment_status) }}</span></template></el-table-column>
        <el-table-column label="最高风险" width="130"><template #default="{ row }"><RiskBadge :level="row.highest_risk_level" /></template></el-table-column>
        <el-table-column prop="algorithm_version" label="算法 / 阈值" min-width="170" />
        <el-table-column label="提交时间" width="165"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
        <el-table-column label="操作" min-width="190" fixed="right"><template #default="{ row }"><el-button :icon="Eye" text @click="inspect(row)">查看</el-button><el-button v-if="auth.canEdit.value && row.assessment_status === 'queued'" type="primary" text :icon="Play" :loading="workingId === row.id" @click="runOne(row)">运行</el-button><template v-if="auth.canReview.value && row.assessment_status === 'pending_review'"><el-button type="success" text :icon="Check" @click="openReview(row, 'accepted')">接受</el-button><el-button type="danger" text :icon="X" @click="openReview(row, 'rejected')">拒绝</el-button></template></template></el-table-column>
        <template #empty><div class="empty-state"><div><strong>暂无评估记录</strong><span>从 active 路线提交第一条评估</span></div></div></template>
      </el-table>
    </div>
  </section>

  <el-dialog v-model="createDialog" title="提交评估" width="520px"><el-form label-position="top"><el-form-item label="active 工艺路线" required><el-select v-model="routeId" filterable style="width: 100%"><el-option v-for="route in routes.filter((item) => item.route_status === 'active')" :key="route.id" :label="`${route.route_code} · ${route.product_name} · v${route.version}`" :value="route.id" /></el-select></el-form-item></el-form><template #footer><el-button @click="createDialog = false">取消</el-button><el-button type="primary" :disabled="!routeId" @click="createRun">进入队列</el-button></template></el-dialog>

  <el-drawer v-model="detailOpen" title="评估证据" size="min(760px, 94vw)">
    <template v-if="selected">
      <div class="assessment-head"><div><span>评估 #{{ selected.id }}</span><strong>{{ routesById[selected.route_id]?.route_code }}</strong></div><span class="status-pill" :class="selected.assessment_status">{{ assessmentLabels[selected.assessment_status] }}</span><RiskBadge :level="selected.highest_risk_level" /></div>
      <dl class="run-facts"><div><dt>算法版本</dt><dd>{{ selected.algorithm_version }}</dd></div><div><dt>完成时间</dt><dd>{{ dateTime(selected.completed_at) }}</dd></div><div><dt>复核意见</dt><dd>{{ selected.review_reason || '—' }}</dd></div></dl>
      <div v-if="selected.superseded_by_id" class="lineage-note">该记录已被评估 <button class="link-button mono" @click="inspectById(selected.superseded_by_id)">#{{ selected.superseded_by_id }}</button> 替代，内容保持只读</div>
      <div v-if="selected.recalc_of_id" class="lineage-note">本评估由 <button class="link-button mono" @click="inspectById(selected.recalc_of_id)">#{{ selected.recalc_of_id }}</button> 重算产生</div>
      <section v-if="selected.assessment_status === 'stale' && !selected.superseded_by_id" class="stale-section" v-loading="diffLoading">
        <div class="stale-header"><span>过期归因</span></div>
        <el-alert v-if="staleDiff?.current_result_usable" type="success" :closable="false" title="当前输入与快照逐项一致，现有结果仍然可用" />
        <ul v-else-if="staleDiff" class="diff-list">
          <li v-for="(item, index) in staleDiff.items" :key="index"><span class="diff-tag">{{ diffKindLabels[item.kind] }}</span><strong class="mono">{{ item.code }}</strong><span class="diff-change">{{ diffChangeLabels[item.change] }}</span><span class="diff-values mono">{{ item.before }} → {{ item.after }}</span></li>
        </ul>
      </section>
      <div v-if="auth.canEdit.value && canRecalculate" class="recalc-bar"><el-button type="primary" :icon="RotateCcw" :loading="workingId === selected.id" @click="recalculate">按当前输入重算</el-button><span class="muted">重算生成一条新的待复核评估，本记录保持只读</span></div>
      <div v-if="selected.risk_items_json.length" class="risk-selector"><button v-for="(item, index) in selected.risk_items_json" :key="`${item.allergen}-${item.path.join('-')}`" :class="{ active: index === selectedRiskIndex }" @click="selectedRiskIndex = index"><span>{{ item.allergen }} · {{ item.target_step_code }}</span><RiskBadge :level="item.risk_level" :score="item.raw_score" /></button></div>
      <EvidencePathPanel :item="selected.risk_items_json[selectedRiskIndex]" />
    </template>
  </el-drawer>

  <el-dialog v-model="reviewOpen" :title="reviewDecision === 'accepted' ? '接受评估' : '拒绝评估'" width="540px"><div v-if="selected" class="review-target">评估 #{{ selected.id }} · {{ routesById[selected.route_id]?.route_code }}</div><el-form label-position="top"><el-form-item label="复核理由" required><el-input v-model="reviewReason" type="textarea" :rows="4" maxlength="1000" show-word-limit placeholder="记录证据判断与后续处置依据" /></el-form-item></el-form><template #footer><el-button @click="reviewOpen = false">取消</el-button><el-button :type="reviewDecision === 'accepted' ? 'success' : 'danger'" :loading="workingId === selected?.id" :disabled="reviewReason.trim().length < 4" @click="review">确认{{ reviewDecision === 'accepted' ? '接受' : '拒绝' }}</el-button></template></el-dialog>
</template>

<style scoped>
.row-sub { margin-top: 3px; font-size: 11px; }.assessment-head { display: flex; align-items: center; gap: 10px; padding: 12px; background: var(--surface-muted); border: 1px solid var(--line); }.assessment-head > div { margin-right: auto; }.assessment-head div span, .assessment-head div strong { display: block; }.assessment-head div span { color: var(--muted); font-size: 10px; }.assessment-head div strong { margin-top: 3px; }.run-facts { display: grid; grid-template-columns: 1fr 1fr 1.5fr; margin: 12px 0; border: 1px solid var(--line); }.run-facts div { padding: 9px 11px; border-right: 1px solid var(--line); }.run-facts div:last-child { border-right: 0; }.run-facts dt { color: var(--muted); font-size: 10px; }.run-facts dd { margin: 4px 0 0; font-size: 11px; }.lineage-note { margin-bottom: 12px; padding: 9px 12px; background: var(--surface-muted); border: 1px solid var(--line); font-size: 12px; }.stale-section { margin-bottom: 12px; border: 1px solid var(--line); }.stale-header { display: flex; align-items: center; padding: 7px 11px; background: var(--surface-muted); border-bottom: 1px solid var(--line); font-size: 11px; font-weight: 800; }.stale-section .el-alert { border-radius: 0; }.diff-list { margin: 0; padding: 0; list-style: none; }.diff-list li { display: flex; align-items: baseline; gap: 8px; padding: 8px 11px; border-bottom: 1px solid var(--line); font-size: 12px; }.diff-list li:last-child { border-bottom: 0; }.diff-tag { flex-shrink: 0; padding: 1px 7px; background: #f4e9dc; color: #7a4b1d; border-radius: 3px; font-size: 10px; font-weight: 700; }.diff-change { flex-shrink: 0; color: var(--muted); font-size: 11px; }.diff-values { margin-left: auto; color: var(--muted); font-size: 11px; text-align: right; word-break: break-all; }.recalc-bar { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; padding: 10px 12px; background: var(--surface-muted); border: 1px solid var(--line); }.recalc-bar .muted { font-size: 11px; }.risk-selector { max-height: 180px; overflow: auto; display: grid; margin-bottom: 12px; border: 1px solid var(--line); }.risk-selector button { min-height: 42px; display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 6px 9px; color: var(--text); background: var(--surface); border: 0; border-bottom: 1px solid var(--line); cursor: pointer; }.risk-selector button:last-child { border-bottom: 0; }.risk-selector button.active { background: #eaf2ee; box-shadow: inset 3px 0 var(--accent); }.review-target { margin-bottom: 16px; padding: 10px 12px; background: var(--surface-muted); border: 1px solid var(--line); font: 12px "SFMono-Regular", Consolas, monospace; }
@media (max-width: 600px) { .run-facts { grid-template-columns: 1fr; }.run-facts div { border-right: 0; border-bottom: 1px solid var(--line); }.run-facts div:last-child { border-bottom: 0; }.assessment-head { align-items: flex-start; flex-wrap: wrap; } }
</style>
