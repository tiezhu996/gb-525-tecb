import type { RiskLevel } from './risk'

export type AssessmentStatus = 'queued' | 'calculating' | 'pending_review' | 'accepted' | 'rejected' | 'stale'

export const assessmentLabels: Record<AssessmentStatus, string> = {
  queued: '待计算',
  calculating: '计算中',
  pending_review: '待复核',
  accepted: '已接受',
  rejected: '已拒绝',
  stale: '已过期',
}

export interface AssessmentRun {
  id: number
  route_id: number
  assessment_status: AssessmentStatus
  input_snapshot_json: Record<string, unknown>
  matrix_json: MatrixCell[]
  risk_items_json: RiskItem[]
  highest_risk_level: RiskLevel
  algorithm_version: string
  created_by: number
  reviewed_by?: number
  review_reason: string
  completed_at?: string
  reviewed_at?: string
  superseded_by_id?: number
  created_at: string
}

export type ProfileInputChangeType = 'version_changed' | 'removed'
export type RouteStepInputChangeType = 'added' | 'removed' | 'modified'
export type ContactEdgeInputChangeType = 'added' | 'removed' | 'version_changed'
export type DeclaredAllergenChangeType = 'added' | 'removed'

export interface ProfileInputChange {
  profile_id: number
  profile_code: string
  material_name: string
  snapshot_version: number
  current_version: number
  change_type: ProfileInputChangeType
}

export interface RouteStepInputChange {
  step_code: string
  change_type: RouteStepInputChangeType
  snapshot_step_name?: string
  current_step_name?: string
  snapshot_profile_id: number
  current_profile_id: number
  snapshot_profile_code?: string
  current_profile_code?: string
  snapshot_order: number
  current_order: number
  reordered: boolean
}

export interface ContactEdgeInputChange {
  edge_id: number
  from_step_code: string
  to_step_code: string
  change_type: ContactEdgeInputChangeType
  snapshot_version: number
  current_version: number
  snapshot_enabled?: boolean
  current_enabled?: boolean
}

export interface DeclaredAllergenChange {
  allergen: string
  change_type: DeclaredAllergenChangeType
}

export interface RouteInputChange {
  snapshot_version: number
  current_version: number
  version_changed: boolean
  declared_allergen_changes: DeclaredAllergenChange[]
}

export interface AssessmentInputDiff {
  assessment_id: number
  assessment_status: AssessmentStatus
  route_status: string
  route_available: boolean
  profile_changes: ProfileInputChange[]
  route_step_changes: RouteStepInputChange[]
  contact_edge_changes: ContactEdgeInputChange[]
  route_change?: RouteInputChange
  has_changes: boolean
  recompute_available: boolean
  recompute_reason?: string
  superseded_by_id?: number
}

export interface EdgeEvidence {
  edge_id: number
  from: string
  to: string
  contact_type: string
  shared_equipment: string
  cleaning_factor: number
  carryover_probability: number
  edge_weight: number
  evidence_note: string
  edge_version: number
}

export interface RiskItem {
  allergen: string
  source_profile_id: number
  source_profile_code: string
  source_material: string
  source_step_code: string
  target_step_code: string
  target_step_name: string
  path: string[]
  raw_score: number
  risk_level: RiskLevel
  declared: boolean
  critical_edge?: EdgeEvidence
  cleaning_evidence: EdgeEvidence[]
  threshold_version: string
}

export interface MatrixCell {
  target_step_code: string
  target_step_name: string
  allergen: string
  max_raw_score: number
  risk_level: RiskLevel
  path_count: number
  declared: boolean
}
