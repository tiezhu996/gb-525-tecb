package dto

type ProfileInputChange struct {
	ProfileID       uint   `json:"profile_id"`
	ProfileCode     string `json:"profile_code"`
	MaterialName    string `json:"material_name"`
	SnapshotVersion uint   `json:"snapshot_version"`
	CurrentVersion  uint   `json:"current_version"`
	ChangeType      string `json:"change_type"`
}

type RouteStepInputChange struct {
	StepCode            string `json:"step_code"`
	ChangeType          string `json:"change_type"`
	SnapshotStepName    string `json:"snapshot_step_name,omitempty"`
	CurrentStepName     string `json:"current_step_name,omitempty"`
	SnapshotProfileID   uint   `json:"snapshot_profile_id"`
	CurrentProfileID    uint   `json:"current_profile_id"`
	SnapshotProfileCode string `json:"snapshot_profile_code,omitempty"`
	CurrentProfileCode  string `json:"current_profile_code,omitempty"`
	SnapshotOrder       int    `json:"snapshot_order"`
	CurrentOrder        int    `json:"current_order"`
	Reordered           bool   `json:"reordered"`
}

type ContactEdgeInputChange struct {
	EdgeID          uint   `json:"edge_id"`
	FromStepCode    string `json:"from_step_code"`
	ToStepCode      string `json:"to_step_code"`
	ChangeType      string `json:"change_type"`
	SnapshotVersion uint   `json:"snapshot_version"`
	CurrentVersion  uint   `json:"current_version"`
	SnapshotEnabled *bool  `json:"snapshot_enabled"`
	CurrentEnabled  *bool  `json:"current_enabled"`
}

type DeclaredAllergenChange struct {
	Allergen   string `json:"allergen"`
	ChangeType string `json:"change_type"`
}

type RouteInputChange struct {
	SnapshotVersion uint                     `json:"snapshot_version"`
	CurrentVersion  uint                     `json:"current_version"`
	VersionChanged  bool                     `json:"version_changed"`
	DeclaredChanges []DeclaredAllergenChange `json:"declared_allergen_changes"`
}

type AssessmentInputDiff struct {
	AssessmentID       uint                     `json:"assessment_id"`
	AssessmentStatus   string                   `json:"assessment_status"`
	RouteStatus        string                   `json:"route_status"`
	RouteAvailable     bool                     `json:"route_available"`
	ProfileChanges     []ProfileInputChange     `json:"profile_changes"`
	RouteStepChanges   []RouteStepInputChange   `json:"route_step_changes"`
	ContactEdgeChanges []ContactEdgeInputChange `json:"contact_edge_changes"`
	RouteChange        *RouteInputChange        `json:"route_change"`
	HasChanges         bool                     `json:"has_changes"`
	RecomputeAvailable bool                     `json:"recompute_available"`
	RecomputeReason    string                   `json:"recompute_reason,omitempty"`
	SupersededByID     *uint                    `json:"superseded_by_id"`
}
