package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"food-allergen-crosscontact-analyzer/backend/internal/analyzer"
	"food-allergen-crosscontact-analyzer/backend/internal/config"
	"food-allergen-crosscontact-analyzer/backend/internal/constants"
	"food-allergen-crosscontact-analyzer/backend/internal/dto"
	"food-allergen-crosscontact-analyzer/backend/internal/model"
	"food-allergen-crosscontact-analyzer/backend/internal/repository"
	"gorm.io/datatypes"
)

type AssessmentService struct {
	runs       repository.AssessmentRepository
	routes     repository.RouteRepository
	profiles   repository.ProfileRepository
	edges      repository.ContactEdgeRepository
	maxDepth   int
	thresholds analyzer.ThresholdSnapshot
	algorithm  string
}

type queuedAssessmentSnapshot struct {
	RouteID             uint `json:"route_id"`
	RouteVersionAtQueue uint `json:"route_version_at_queue"`
}

type completedSnapshot struct {
	Route    snapshotRoute    `json:"route"`
	Profiles []snapshotEntity `json:"profiles"`
	Edges    []snapshotEdge   `json:"contact_edges"`
}

type snapshotRoute struct {
	ID       uint            `json:"id"`
	Code     string          `json:"code"`
	Version  uint            `json:"version"`
	Steps    []dto.RouteStep `json:"steps"`
	Declared []string        `json:"declared_allergens"`
}

type snapshotEntity struct {
	ID      uint   `json:"id"`
	Code    string `json:"code"`
	Version uint   `json:"version"`
}

type snapshotEdge struct {
	ID      uint `json:"id"`
	Version uint `json:"version"`
	Enabled bool `json:"enabled"`
}

func NewAssessmentService(runs repository.AssessmentRepository, routes repository.RouteRepository, profiles repository.ProfileRepository, edges repository.ContactEdgeRepository, cfg config.Config) (*AssessmentService, error) {
	thresholds, err := analyzer.NewThresholdSnapshot(cfg.Thresholds)
	if err != nil {
		return nil, fmt.Errorf("initialize thresholds: %w", err)
	}
	return &AssessmentService{runs: runs, routes: routes, profiles: profiles, edges: edges, maxDepth: cfg.MaxPropagationDepth, thresholds: thresholds, algorithm: "weighted-path-v1/" + thresholds.Version}, nil
}

func (s *AssessmentService) Preview(ctx context.Context, routeID uint) (analyzer.Result, error) {
	result, _, err := s.compute(ctx, routeID)
	return result, err
}

func (s *AssessmentService) Create(ctx context.Context, request dto.CreateAssessmentRequest, actor Principal, requestID string) (model.AssessmentRun, error) {
	route, err := s.routes.Get(ctx, request.RouteID)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	if route.RouteStatus != "active" {
		return model.AssessmentRun{}, NewError(http.StatusConflict, "route_inactive", "只有 active 路线可以提交评估", nil)
	}
	queuedSnapshot, err := json.Marshal(map[string]any{"route_id": route.ID, "route_version_at_queue": route.Version, "queued_at": time.Now().UTC(), "threshold_version": s.thresholds.Version})
	if err != nil {
		return model.AssessmentRun{}, fmt.Errorf("encode queued snapshot: %w", err)
	}
	run := model.AssessmentRun{RouteID: route.ID, AssessmentStatus: constants.AssessmentQueued, InputSnapshotJSON: datatypes.JSON(queuedSnapshot), MatrixJSON: datatypes.JSON([]byte("[]")), RiskItemsJSON: datatypes.JSON([]byte("[]")), HighestRiskLevel: constants.RiskLow, AlgorithmVersion: s.algorithm, CreatedBy: actor.ID}
	if err := s.runs.Create(ctx, &run, AuditScope(actor, requestID)); err != nil {
		return model.AssessmentRun{}, err
	}
	return run, nil
}

func (s *AssessmentService) Run(ctx context.Context, id uint, actor Principal, requestID string) (model.AssessmentRun, error) {
	if err := s.runs.BeginCalculation(ctx, id, AuditScope(actor, requestID)); err != nil {
		return model.AssessmentRun{}, err
	}
	run, err := s.runs.Get(ctx, id)
	if err != nil {
		s.resetAfterFailure(ctx, id, err, actor, requestID)
		return model.AssessmentRun{}, err
	}
	var queued queuedAssessmentSnapshot
	if err := json.Unmarshal(run.InputSnapshotJSON, &queued); err != nil || queued.RouteID != run.RouteID || queued.RouteVersionAtQueue == 0 {
		validationErr := NewError(http.StatusUnprocessableEntity, "assessment_snapshot_invalid", "评估排队快照无效，请重新提交评估", err)
		s.resetAfterFailure(ctx, id, validationErr, actor, requestID)
		return model.AssessmentRun{}, validationErr
	}
	route, err := s.routes.Get(ctx, run.RouteID)
	if err != nil {
		s.resetAfterFailure(ctx, id, err, actor, requestID)
		return model.AssessmentRun{}, err
	}
	if route.Version != queued.RouteVersionAtQueue {
		conflictErr := NewError(http.StatusConflict, "route_version_conflict", "路线在评估排队后已变化，请重新提交评估", nil)
		s.resetAfterFailure(ctx, id, conflictErr, actor, requestID)
		return model.AssessmentRun{}, conflictErr
	}
	result, snapshot, err := s.computeRoute(ctx, route)
	if err != nil {
		s.resetAfterFailure(ctx, id, err, actor, requestID)
		return model.AssessmentRun{}, err
	}
	matrixJSON, err := json.Marshal(result.Matrix)
	if err != nil {
		s.resetAfterFailure(ctx, id, err, actor, requestID)
		return model.AssessmentRun{}, fmt.Errorf("encode assessment matrix: %w", err)
	}
	riskJSON, err := json.Marshal(result.RiskItems)
	if err != nil {
		s.resetAfterFailure(ctx, id, err, actor, requestID)
		return model.AssessmentRun{}, fmt.Errorf("encode assessment risk items: %w", err)
	}
	if err := s.runs.CompleteCalculation(ctx, id, snapshot, datatypes.JSON(matrixJSON), datatypes.JSON(riskJSON), result.HighestRiskLevel, s.algorithm, AuditScope(actor, requestID)); err != nil {
		return model.AssessmentRun{}, err
	}
	return s.runs.Get(ctx, id)
}

func (s *AssessmentService) Review(ctx context.Context, id uint, request dto.ReviewAssessmentRequest, actor Principal, requestID string) (model.AssessmentRun, error) {
	if !CanReview(actor.Role) {
		return model.AssessmentRun{}, NewError(http.StatusForbidden, "forbidden", "仅 reviewer 或 admin 可复核评估", nil)
	}
	target := constants.AssessmentStatus(request.Decision)
	if target != constants.AssessmentAccepted && target != constants.AssessmentRejected {
		return model.AssessmentRun{}, NewError(http.StatusBadRequest, "invalid_decision", "复核决定无效", nil)
	}
	if err := s.runs.Review(ctx, id, target, actor.ID, request.Reason, AuditScope(actor, requestID)); err != nil {
		return model.AssessmentRun{}, err
	}
	return s.runs.Get(ctx, id)
}

func (s *AssessmentService) Get(ctx context.Context, id uint) (model.AssessmentRun, error) {
	return s.runs.Get(ctx, id)
}

func (s *AssessmentService) StaleDiff(ctx context.Context, id uint) (dto.StaleDiff, error) {
	run, err := s.runs.Get(ctx, id)
	if err != nil {
		return dto.StaleDiff{}, err
	}
	items, err := s.snapshotDiff(ctx, run)
	if err != nil {
		return dto.StaleDiff{}, err
	}
	return dto.StaleDiff{AssessmentID: run.ID, AssessmentStatus: string(run.AssessmentStatus), Items: items, CurrentResultUsable: len(items) == 0}, nil
}

func (s *AssessmentService) Recalculate(ctx context.Context, id uint, actor Principal, requestID string) (model.AssessmentRun, error) {
	original, err := s.runs.Get(ctx, id)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	if original.AssessmentStatus != constants.AssessmentStale && original.AssessmentStatus != constants.AssessmentRejected {
		return model.AssessmentRun{}, NewError(http.StatusConflict, "state_conflict", "仅已过期或被拒绝的评估可以重算", nil)
	}
	if original.SupersededByID != nil {
		return model.AssessmentRun{}, NewError(http.StatusConflict, "state_conflict", "该评估已被更新的评估替代", nil)
	}
	route, err := s.routes.Get(ctx, original.RouteID)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	if route.RouteStatus != "active" {
		return model.AssessmentRun{}, NewError(http.StatusConflict, "route_inactive", "只有 active 路线可以重算评估", nil)
	}
	diff, err := s.snapshotDiff(ctx, original)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	result, snapshot, err := s.computeRoute(ctx, route)
	if err != nil {
		return model.AssessmentRun{}, err
	}
	matrixJSON, err := json.Marshal(result.Matrix)
	if err != nil {
		return model.AssessmentRun{}, fmt.Errorf("encode assessment matrix: %w", err)
	}
	riskJSON, err := json.Marshal(result.RiskItems)
	if err != nil {
		return model.AssessmentRun{}, fmt.Errorf("encode assessment risk items: %w", err)
	}
	now := time.Now().UTC()
	run := model.AssessmentRun{RouteID: route.ID, AssessmentStatus: constants.AssessmentPendingReview, InputSnapshotJSON: snapshot, MatrixJSON: datatypes.JSON(matrixJSON), RiskItemsJSON: datatypes.JSON(riskJSON), HighestRiskLevel: result.HighestRiskLevel, AlgorithmVersion: s.algorithm, CreatedBy: actor.ID, RecalcOfID: &original.ID, CompletedAt: &now}
	if err := s.runs.Recalculate(ctx, original.ID, &run, diff, AuditScope(actor, requestID)); err != nil {
		return model.AssessmentRun{}, err
	}
	return s.runs.Get(ctx, run.ID)
}
func (s *AssessmentService) List(ctx context.Context, query dto.AssessmentQuery) ([]model.AssessmentRun, int64, error) {
	return s.runs.List(ctx, query)
}
func (s *AssessmentService) Summary(ctx context.Context) (dto.AssessmentSummary, error) {
	return s.runs.Summary(ctx)
}

func (s *AssessmentService) compute(ctx context.Context, routeID uint) (analyzer.Result, datatypes.JSON, error) {
	route, err := s.routes.Get(ctx, routeID)
	if err != nil {
		return analyzer.Result{}, nil, err
	}
	return s.computeRoute(ctx, route)
}

func (s *AssessmentService) computeRoute(ctx context.Context, route model.ProcessRoute) (analyzer.Result, datatypes.JSON, error) {
	steps, err := DecodeRouteSteps(route)
	if err != nil {
		return analyzer.Result{}, nil, err
	}
	declared, err := DecodeDeclared(route)
	if err != nil {
		return analyzer.Result{}, nil, err
	}
	edges, err := s.edges.ForRoute(ctx, route.ID)
	if err != nil {
		return analyzer.Result{}, nil, err
	}
	ids := make([]uint, 0, len(steps))
	seen := make(map[uint]bool)
	for _, step := range steps {
		if !seen[step.ProfileID] {
			seen[step.ProfileID] = true
			ids = append(ids, step.ProfileID)
		}
	}
	profiles, err := s.profiles.GetMany(ctx, ids)
	if err != nil {
		return analyzer.Result{}, nil, err
	}
	if len(profiles) != len(ids) {
		return analyzer.Result{}, nil, NewError(http.StatusUnprocessableEntity, "profile_missing", "路线引用的过敏原谱已不可用", nil)
	}
	seeds := make(map[uint]analyzer.ProfileSeed, len(profiles))
	profileVersions := make([]map[string]any, 0, len(profiles))
	for _, profile := range profiles {
		var allergens []string
		if err := json.Unmarshal(profile.AllergensJSON, &allergens); err != nil {
			return analyzer.Result{}, nil, NewError(http.StatusUnprocessableEntity, "profile_json_invalid", "过敏原谱内容无法解析", err)
		}
		seeds[profile.ID] = analyzer.ProfileSeed{ProfileID: profile.ID, ProfileCode: profile.ProfileCode, MaterialName: profile.MaterialName, Version: profile.Version, Allergens: allergens}
		profileVersions = append(profileVersions, map[string]any{"id": profile.ID, "code": profile.ProfileCode, "version": profile.Version})
	}
	graph, err := analyzer.BuildGraph(steps, edges)
	if err != nil {
		return analyzer.Result{}, nil, NewError(http.StatusUnprocessableEntity, "graph_invalid", "接触图结构无效", err)
	}
	result, err := analyzer.Propagate(graph, seeds, declared, s.maxDepth, s.thresholds)
	if err != nil {
		return analyzer.Result{}, nil, NewError(http.StatusUnprocessableEntity, "propagation_failed", "风险传播计算失败", err)
	}
	sort.Slice(profileVersions, func(i, j int) bool { return profileVersions[i]["id"].(uint) < profileVersions[j]["id"].(uint) })
	edgeVersions := make([]map[string]any, 0, len(edges))
	for _, edge := range edges {
		edgeVersions = append(edgeVersions, map[string]any{"id": edge.ID, "version": edge.Version, "enabled": edge.Enabled})
	}
	snapshotValue := map[string]any{"captured_at": time.Now().UTC(), "route": map[string]any{"id": route.ID, "code": route.RouteCode, "version": route.Version, "steps": steps, "declared_allergens": declared}, "profiles": profileVersions, "contact_edges": edgeVersions, "thresholds": s.thresholds, "algorithm_version": s.algorithm, "max_depth": s.maxDepth, "cycles": result.Cycles}
	snapshot, err := json.Marshal(snapshotValue)
	if err != nil {
		return analyzer.Result{}, nil, fmt.Errorf("encode input snapshot: %w", err)
	}
	return result, datatypes.JSON(snapshot), nil
}

// snapshotDiff compares the completed input snapshot of a run against the
// current route, profile and contact edge versions and returns only the
// inputs that actually changed.
func (s *AssessmentService) snapshotDiff(ctx context.Context, run model.AssessmentRun) ([]dto.StaleDiffItem, error) {
	var snapshot completedSnapshot
	if err := json.Unmarshal(run.InputSnapshotJSON, &snapshot); err != nil {
		return nil, NewError(http.StatusUnprocessableEntity, "assessment_snapshot_invalid", "评估输入快照无法解析", err)
	}
	if snapshot.Route.ID == 0 {
		return nil, NewError(http.StatusUnprocessableEntity, "assessment_snapshot_incomplete", "评估尚未完成计算，没有可比对的输入快照", nil)
	}
	route, err := s.routes.Get(ctx, run.RouteID)
	if err != nil {
		return nil, err
	}
	steps, err := DecodeRouteSteps(route)
	if err != nil {
		return nil, err
	}
	declared, err := DecodeDeclared(route)
	if err != nil {
		return nil, err
	}
	items := make([]dto.StaleDiffItem, 0)
	stepsChanged := !reflect.DeepEqual(snapshot.Route.Steps, steps)
	snapshotDeclared := normalizeStrings(snapshot.Route.Declared)
	declaredChanged := !reflect.DeepEqual(snapshotDeclared, declared)
	if stepsChanged {
		items = append(items, dto.StaleDiffItem{Kind: "route", Code: route.RouteCode, Change: "steps_changed", Before: summarizeSteps(snapshot.Route.Steps), After: summarizeSteps(steps)})
	}
	if declaredChanged {
		items = append(items, dto.StaleDiffItem{Kind: "route", Code: route.RouteCode, Change: "declared_changed", Before: strings.Join(snapshotDeclared, ", "), After: strings.Join(declared, ", ")})
	}
	if !stepsChanged && !declaredChanged && snapshot.Route.Version != route.Version {
		items = append(items, dto.StaleDiffItem{Kind: "route", Code: route.RouteCode, Change: "version_changed", Before: fmt.Sprintf("v%d", snapshot.Route.Version), After: fmt.Sprintf("v%d", route.Version)})
	}
	profileIDs := make([]uint, 0, len(steps))
	seenProfiles := make(map[uint]bool)
	for _, step := range steps {
		if !seenProfiles[step.ProfileID] {
			seenProfiles[step.ProfileID] = true
			profileIDs = append(profileIDs, step.ProfileID)
		}
	}
	profiles, err := s.profiles.GetMany(ctx, profileIDs)
	if err != nil {
		return nil, err
	}
	currentProfiles := make(map[uint]model.AllergenProfile, len(profiles))
	for _, profile := range profiles {
		currentProfiles[profile.ID] = profile
	}
	snapshotProfiles := make(map[uint]bool, len(snapshot.Profiles))
	for _, snap := range snapshot.Profiles {
		snapshotProfiles[snap.ID] = true
		current, ok := currentProfiles[snap.ID]
		if !ok {
			items = append(items, dto.StaleDiffItem{Kind: "profile", Code: snap.Code, Change: "removed", Before: fmt.Sprintf("v%d", snap.Version), After: "不再被路线引用"})
			continue
		}
		if current.Version != snap.Version {
			items = append(items, dto.StaleDiffItem{Kind: "profile", Code: snap.Code, Change: "version_changed", Before: fmt.Sprintf("v%d", snap.Version), After: fmt.Sprintf("v%d", current.Version)})
		}
	}
	addedProfiles := make([]model.AllergenProfile, 0)
	for _, profile := range profiles {
		if !snapshotProfiles[profile.ID] {
			addedProfiles = append(addedProfiles, profile)
		}
	}
	sort.Slice(addedProfiles, func(i, j int) bool { return addedProfiles[i].ID < addedProfiles[j].ID })
	for _, profile := range addedProfiles {
		items = append(items, dto.StaleDiffItem{Kind: "profile", Code: profile.ProfileCode, Change: "added", Before: "—", After: fmt.Sprintf("v%d", profile.Version)})
	}
	edges, err := s.edges.ForRoute(ctx, run.RouteID)
	if err != nil {
		return nil, err
	}
	currentEdges := make(map[uint]model.ContactEdge, len(edges))
	for _, edge := range edges {
		currentEdges[edge.ID] = edge
	}
	snapshotEdges := make(map[uint]bool, len(snapshot.Edges))
	for _, snap := range snapshot.Edges {
		snapshotEdges[snap.ID] = true
		current, ok := currentEdges[snap.ID]
		if !ok {
			items = append(items, dto.StaleDiffItem{Kind: "contact_edge", Code: fmt.Sprintf("边 #%d", snap.ID), Change: "removed", Before: edgeVersionSummary(snap.Version, snap.Enabled), After: "已删除"})
			continue
		}
		if current.Version != snap.Version || current.Enabled != snap.Enabled {
			items = append(items, dto.StaleDiffItem{Kind: "contact_edge", Code: current.FromStepCode + "→" + current.ToStepCode, Change: "version_changed", Before: edgeVersionSummary(snap.Version, snap.Enabled), After: edgeVersionSummary(current.Version, current.Enabled)})
		}
	}
	for _, edge := range edges {
		if !snapshotEdges[edge.ID] {
			items = append(items, dto.StaleDiffItem{Kind: "contact_edge", Code: edge.FromStepCode + "→" + edge.ToStepCode, Change: "added", Before: "—", After: edgeVersionSummary(edge.Version, edge.Enabled)})
		}
	}
	return items, nil
}

func summarizeSteps(steps []dto.RouteStep) string {
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		parts = append(parts, fmt.Sprintf("%s(谱#%d)", step.StepCode, step.ProfileID))
	}
	return strings.Join(parts, " → ")
}

func edgeVersionSummary(version uint, enabled bool) string {
	return fmt.Sprintf("v%d · enabled=%t", version, enabled)
}

func (s *AssessmentService) resetAfterFailure(ctx context.Context, id uint, calculationErr error, actor Principal, requestID string) {
	_ = s.runs.ResetCalculation(ctx, id, calculationErr.Error(), AuditScope(actor, requestID))
}
