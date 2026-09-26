package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"food-allergen-crosscontact-analyzer/backend/internal/constants"
	"food-allergen-crosscontact-analyzer/backend/internal/dto"
	"food-allergen-crosscontact-analyzer/backend/internal/model"
	"food-allergen-crosscontact-analyzer/backend/internal/repository"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type completedSnapshotProfile struct {
	ID      uint   `json:"id"`
	Code    string `json:"code"`
	Version uint   `json:"version"`
}

type completedSnapshotEdge struct {
	ID           uint   `json:"id"`
	Version      uint   `json:"version"`
	Enabled      bool   `json:"enabled"`
	FromStepCode string `json:"from_step_code"`
	ToStepCode   string `json:"to_step_code"`
}

type completedSnapshotRoute struct {
	ID                uint            `json:"id"`
	Code              string          `json:"code"`
	Version           uint            `json:"version"`
	Steps             []dto.RouteStep `json:"steps"`
	DeclaredAllergens []string        `json:"declared_allergens"`
}

type completedSnapshot struct {
	CapturedAt   time.Time                  `json:"captured_at"`
	Route        completedSnapshotRoute     `json:"route"`
	Profiles     []completedSnapshotProfile `json:"profiles"`
	ContactEdges []completedSnapshotEdge    `json:"contact_edges"`
}

func decodeCompletedSnapshot(run model.AssessmentRun) (completedSnapshot, error) {
	var snapshot completedSnapshot
	if err := json.Unmarshal(run.InputSnapshotJSON, &snapshot); err != nil {
		return completedSnapshot{}, NewError(http.StatusUnprocessableEntity, "assessment_snapshot_invalid", "评估输入快照无法解析", err)
	}
	if snapshot.Route.ID != run.RouteID || snapshot.Route.Version == 0 {
		return completedSnapshot{}, NewError(http.StatusUnprocessableEntity, "assessment_snapshot_invalid", "评估输入快照缺少有效的路线版本", nil)
	}
	return snapshot, nil
}

// Diff 逐项比对评估快照里的过敏原谱、路线步骤、接触边版本和路线声明，
// 只返回确实发生变化的输入项；没有差异时给出的重算理由为空。
func (s *AssessmentService) Diff(ctx context.Context, id uint) (dto.AssessmentInputDiff, error) {
	run, err := s.runs.Get(ctx, id)
	if err != nil {
		return dto.AssessmentInputDiff{}, err
	}
	if run.AssessmentStatus != constants.AssessmentStale && run.AssessmentStatus != constants.AssessmentRejected {
		return dto.AssessmentInputDiff{}, NewError(http.StatusConflict, "state_conflict", "仅已过期或已拒绝的评估可以比对输入差异", nil)
	}
	return s.buildInputDiff(ctx, run)
}

func (s *AssessmentService) buildInputDiff(ctx context.Context, run model.AssessmentRun) (dto.AssessmentInputDiff, error) {
	diff := dto.AssessmentInputDiff{
		AssessmentID:       run.ID,
		AssessmentStatus:   string(run.AssessmentStatus),
		ProfileChanges:     []dto.ProfileInputChange{},
		RouteStepChanges:   []dto.RouteStepInputChange{},
		ContactEdgeChanges: []dto.ContactEdgeInputChange{},
		SupersededByID:     run.SupersededByID,
	}
	snapshot, err := decodeCompletedSnapshot(run)
	if err != nil {
		return dto.AssessmentInputDiff{}, err
	}
	route, err := s.routes.Get(ctx, run.RouteID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			diff.RecomputeReason = "评估引用的工艺路线已不存在，无法按当前输入重算"
			return diff, nil
		}
		return dto.AssessmentInputDiff{}, err
	}
	diff.RouteAvailable = true
	diff.RouteStatus = route.RouteStatus

	currentSteps, err := DecodeRouteSteps(route)
	if err != nil {
		return dto.AssessmentInputDiff{}, err
	}
	currentDeclared, err := DecodeDeclared(route)
	if err != nil {
		return dto.AssessmentInputDiff{}, err
	}
	currentEdges, err := s.edges.ForRoute(ctx, route.ID)
	if err != nil {
		return dto.AssessmentInputDiff{}, err
	}
	profileIDs := make(map[uint]bool)
	for _, profile := range snapshot.Profiles {
		profileIDs[profile.ID] = true
	}
	for _, step := range currentSteps {
		profileIDs[step.ProfileID] = true
	}
	ids := make([]uint, 0, len(profileIDs))
	for id := range profileIDs {
		ids = append(ids, id)
	}
	loadedProfiles, err := s.profiles.GetMany(ctx, ids)
	if err != nil {
		return dto.AssessmentInputDiff{}, err
	}
	currentProfiles := make(map[uint]model.AllergenProfile, len(loadedProfiles))
	for _, profile := range loadedProfiles {
		currentProfiles[profile.ID] = profile
	}
	snapshotProfiles := make(map[uint]completedSnapshotProfile, len(snapshot.Profiles))
	for _, profile := range snapshot.Profiles {
		snapshotProfiles[profile.ID] = profile
	}

	// 过敏原谱：只比对快照内引用过、且版本变化或已删除的谱。
	for _, snapProfile := range snapshot.Profiles {
		current, exists := currentProfiles[snapProfile.ID]
		if !exists {
			diff.ProfileChanges = append(diff.ProfileChanges, dto.ProfileInputChange{ProfileID: snapProfile.ID, ProfileCode: snapProfile.Code, SnapshotVersion: snapProfile.Version, ChangeType: "removed"})
			continue
		}
		if current.Version != snapProfile.Version {
			diff.ProfileChanges = append(diff.ProfileChanges, dto.ProfileInputChange{ProfileID: current.ID, ProfileCode: current.ProfileCode, MaterialName: current.MaterialName, SnapshotVersion: snapProfile.Version, CurrentVersion: current.Version, ChangeType: "version_changed"})
		}
	}

	// 路线步骤：新增、删除、改名、换谱或顺序变化逐项列出。
	snapshotStepMap := make(map[string]dto.RouteStep, len(snapshot.Route.Steps))
	for index, step := range snapshot.Route.Steps {
		snapshotStepMap[step.StepCode] = snapshot.Route.Steps[index]
	}
	currentStepMap := make(map[string]dto.RouteStep, len(currentSteps))
	for index, step := range currentSteps {
		currentStepMap[step.StepCode] = currentSteps[index]
	}
	for index, currentStep := range currentSteps {
		snapStep, existed := snapshotStepMap[currentStep.StepCode]
		if !existed {
			profile := currentProfiles[currentStep.ProfileID]
			diff.RouteStepChanges = append(diff.RouteStepChanges, dto.RouteStepInputChange{StepCode: currentStep.StepCode, ChangeType: "added", CurrentStepName: currentStep.StepName, CurrentProfileID: currentStep.ProfileID, CurrentProfileCode: profile.ProfileCode, SnapshotOrder: -1, CurrentOrder: index})
			continue
		}
		snapOrder := indexOfStep(snapshot.Route.Steps, currentStep.StepCode)
		changedName := currentStep.StepName != snapStep.StepName
		changedProfile := currentStep.ProfileID != snapStep.ProfileID
		reordered := snapOrder != index
		if changedName || changedProfile || reordered {
			change := dto.RouteStepInputChange{StepCode: currentStep.StepCode, ChangeType: "modified", SnapshotStepName: snapStep.StepName, CurrentStepName: currentStep.StepName, SnapshotProfileID: snapStep.ProfileID, CurrentProfileID: currentStep.ProfileID, SnapshotProfileCode: snapshotProfiles[snapStep.ProfileID].Code, CurrentProfileCode: currentProfiles[currentStep.ProfileID].ProfileCode, SnapshotOrder: snapOrder, CurrentOrder: index, Reordered: reordered}
			diff.RouteStepChanges = append(diff.RouteStepChanges, change)
		}
	}
	for index, snapStep := range snapshot.Route.Steps {
		if _, present := currentStepMap[snapStep.StepCode]; present {
			continue
		}
		diff.RouteStepChanges = append(diff.RouteStepChanges, dto.RouteStepInputChange{StepCode: snapStep.StepCode, ChangeType: "removed", SnapshotStepName: snapStep.StepName, SnapshotProfileID: snapStep.ProfileID, SnapshotProfileCode: snapshotProfiles[snapStep.ProfileID].Code, SnapshotOrder: index, CurrentOrder: -1})
	}

	// 接触边：按 ID 比对版本和启用状态，并列出新增与删除的边。
	snapshotEdgeMap := make(map[uint]completedSnapshotEdge, len(snapshot.ContactEdges))
	for _, edge := range snapshot.ContactEdges {
		snapshotEdgeMap[edge.ID] = edge
	}
	currentEdgeMap := make(map[uint]model.ContactEdge, len(currentEdges))
	for _, edge := range currentEdges {
		currentEdgeMap[edge.ID] = edge
	}
	for _, edge := range currentEdges {
		snapEdge, existed := snapshotEdgeMap[edge.ID]
		if !existed {
			enabled := edge.Enabled
			diff.ContactEdgeChanges = append(diff.ContactEdgeChanges, dto.ContactEdgeInputChange{EdgeID: edge.ID, FromStepCode: edge.FromStepCode, ToStepCode: edge.ToStepCode, ChangeType: "added", CurrentVersion: edge.Version, CurrentEnabled: &enabled})
			continue
		}
		if edge.Version != snapEdge.Version || edge.Enabled != snapEdge.Enabled {
			currentEnabled := edge.Enabled
			snapshotEnabled := snapEdge.Enabled
			diff.ContactEdgeChanges = append(diff.ContactEdgeChanges, dto.ContactEdgeInputChange{EdgeID: edge.ID, FromStepCode: edge.FromStepCode, ToStepCode: edge.ToStepCode, ChangeType: "version_changed", SnapshotVersion: snapEdge.Version, CurrentVersion: edge.Version, SnapshotEnabled: &snapshotEnabled, CurrentEnabled: &currentEnabled})
		}
	}
	for _, snapEdge := range snapshot.ContactEdges {
		if _, present := currentEdgeMap[snapEdge.ID]; present {
			continue
		}
		enabled := snapEdge.Enabled
		diff.ContactEdgeChanges = append(diff.ContactEdgeChanges, dto.ContactEdgeInputChange{EdgeID: snapEdge.ID, FromStepCode: snapEdge.FromStepCode, ToStepCode: snapEdge.ToStepCode, ChangeType: "removed", SnapshotVersion: snapEdge.Version, SnapshotEnabled: &enabled})
	}

	// 路线声明过敏原的增删。
	declaredChanges := diffDeclaredAllergens(snapshot.Route.DeclaredAllergens, currentDeclared)
	diff.RouteChange = &dto.RouteInputChange{SnapshotVersion: snapshot.Route.Version, CurrentVersion: route.Version, VersionChanged: route.Version != snapshot.Route.Version, DeclaredChanges: declaredChanges}

	diff.HasChanges = len(diff.ProfileChanges) > 0 || len(diff.RouteStepChanges) > 0 || len(diff.ContactEdgeChanges) > 0 || len(declaredChanges) > 0

	switch {
	case run.SupersededByID != nil:
		diff.RecomputeReason = "该评估已被新评估替代，记录只读"
	case route.RouteStatus != "active":
		diff.RecomputeReason = "路线当前不是 active 状态，不能重新提交评估"
	case run.AssessmentStatus == constants.AssessmentRejected:
		diff.RecomputeAvailable = true
	case run.AssessmentStatus == constants.AssessmentStale && diff.HasChanges:
		diff.RecomputeAvailable = true
	case run.AssessmentStatus == constants.AssessmentStale:
		diff.RecomputeReason = "快照与当前输入一致，现有评估结果仍然可用，无需重算"
	}
	return diff, nil
}

// Recompute 按当前输入对已过期或已拒绝的评估重新计算，结果作为新的一条
// pending_review 评估返回；原记录保持只读并标记被新评估替代。
func (s *AssessmentService) Recompute(ctx context.Context, id uint, actor Principal, requestID string) (model.AssessmentRun, error) {
	source, err := s.runs.Get(ctx, id)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	if source.AssessmentStatus != constants.AssessmentStale && source.AssessmentStatus != constants.AssessmentRejected {
		return model.AssessmentRun{}, NewError(http.StatusConflict, "state_conflict", "仅已过期或已拒绝的评估可以重算", nil)
	}
	if source.SupersededByID != nil {
		return model.AssessmentRun{}, NewError(http.StatusConflict, "state_conflict", "该评估已被新评估替代，不能再次重算", nil)
	}
	diff, err := s.buildInputDiff(ctx, source)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	if !diff.RecomputeAvailable {
		reason := diff.RecomputeReason
		if reason == "" {
			reason = "当前不满足重算条件"
		}
		return model.AssessmentRun{}, NewError(http.StatusConflict, "recompute_unavailable", reason, nil)
	}
	result, snapshot, err := s.compute(ctx, source.RouteID)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	matrixJSON, err := json.Marshal(result.Matrix)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	riskJSON, err := json.Marshal(result.RiskItems)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	now := time.Now().UTC()
	newRun := model.AssessmentRun{
		RouteID:           source.RouteID,
		AssessmentStatus:  constants.AssessmentPendingReview,
		InputSnapshotJSON: snapshot,
		MatrixJSON:        datatypes.JSON(matrixJSON),
		RiskItemsJSON:     datatypes.JSON(riskJSON),
		HighestRiskLevel:  result.HighestRiskLevel,
		AlgorithmVersion:  s.algorithm,
		CreatedBy:         actor.ID,
		CompletedAt:       &now,
	}
	if err := s.runs.CreateRecompute(ctx, &newRun, source, datatypes.JSON(diffJSON), AuditScope(actor, requestID)); err != nil {
		if errors.Is(err, repository.ErrStateConflict) {
			return model.AssessmentRun{}, NewError(http.StatusConflict, "state_conflict", "评估状态已变化，请刷新后重试", err)
		}
		return model.AssessmentRun{}, err
	}
	return s.runs.Get(ctx, newRun.ID)
}

func indexOfStep(steps []dto.RouteStep, code string) int {
	for index, step := range steps {
		if step.StepCode == code {
			return index
		}
	}
	return -1
}

func diffDeclaredAllergens(snapshot, current []string) []dto.DeclaredAllergenChange {
	snapshotSet := make(map[string]string)
	for _, value := range snapshot {
		snapshotSet[lowerTrim(value)] = value
	}
	currentSet := make(map[string]string)
	for _, value := range current {
		currentSet[lowerTrim(value)] = value
	}
	changes := []dto.DeclaredAllergenChange{}
	for key, value := range currentSet {
		if _, existed := snapshotSet[key]; !existed {
			changes = append(changes, dto.DeclaredAllergenChange{Allergen: value, ChangeType: "added"})
		}
	}
	for key, value := range snapshotSet {
		if _, exists := currentSet[key]; !exists {
			changes = append(changes, dto.DeclaredAllergenChange{Allergen: value, ChangeType: "removed"})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].ChangeType != changes[j].ChangeType {
			return changes[i].ChangeType < changes[j].ChangeType
		}
		return lowerTrim(changes[i].Allergen) < lowerTrim(changes[j].Allergen)
	})
	return changes
}

func lowerTrim(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
