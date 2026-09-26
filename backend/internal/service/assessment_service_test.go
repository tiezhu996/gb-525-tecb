package service

import (
	"context"
	"encoding/json"
	"testing"

	"food-allergen-crosscontact-analyzer/backend/internal/config"
	"food-allergen-crosscontact-analyzer/backend/internal/constants"
	"food-allergen-crosscontact-analyzer/backend/internal/dto"
	"food-allergen-crosscontact-analyzer/backend/internal/model"
	"food-allergen-crosscontact-analyzer/backend/internal/repository"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type runRepoStub struct {
	runs   map[uint]*model.AssessmentRun
	nextID uint
	diff   []dto.StaleDiffItem
}

func (r *runRepoStub) Create(_ context.Context, run *model.AssessmentRun, _ repository.AuditContext) error {
	r.nextID++
	run.ID = r.nextID
	r.runs[run.ID] = run
	return nil
}
func (r *runRepoStub) Get(_ context.Context, id uint) (model.AssessmentRun, error) {
	run, ok := r.runs[id]
	if !ok {
		return model.AssessmentRun{}, gorm.ErrRecordNotFound
	}
	return *run, nil
}
func (r *runRepoStub) List(context.Context, dto.AssessmentQuery) ([]model.AssessmentRun, int64, error) {
	return nil, 0, nil
}
func (r *runRepoStub) Summary(context.Context) (dto.AssessmentSummary, error) {
	return dto.AssessmentSummary{}, nil
}
func (r *runRepoStub) BeginCalculation(context.Context, uint, repository.AuditContext) error { return nil }
func (r *runRepoStub) ResetCalculation(context.Context, uint, string, repository.AuditContext) error {
	return nil
}
func (r *runRepoStub) CompleteCalculation(context.Context, uint, datatypes.JSON, datatypes.JSON, datatypes.JSON, constants.RiskLevel, string, repository.AuditContext) error {
	return nil
}
func (r *runRepoStub) Review(context.Context, uint, constants.AssessmentStatus, uint, string, repository.AuditContext) error {
	return nil
}
func (r *runRepoStub) Recalculate(_ context.Context, originalID uint, run *model.AssessmentRun, diff []dto.StaleDiffItem, _ repository.AuditContext) error {
	original, ok := r.runs[originalID]
	if !ok || original.SupersededByID != nil || (original.AssessmentStatus != constants.AssessmentStale && original.AssessmentStatus != constants.AssessmentRejected) {
		return repository.ErrStateConflict
	}
	r.nextID++
	run.ID = r.nextID
	r.runs[run.ID] = run
	original.SupersededByID = &run.ID
	r.diff = diff
	return nil
}

type routeRepoStub struct{ route model.ProcessRoute }

func (r *routeRepoStub) Create(context.Context, *model.ProcessRoute, repository.AuditContext) error {
	return nil
}
func (r *routeRepoStub) Get(context.Context, uint) (model.ProcessRoute, error) { return r.route, nil }
func (r *routeRepoStub) List(context.Context, dto.RouteQuery) ([]model.ProcessRoute, int64, error) {
	return nil, 0, nil
}
func (r *routeRepoStub) Update(context.Context, *model.ProcessRoute, uint, repository.AuditContext) error {
	return nil
}

type profileRepoStub struct{ profiles map[uint]model.AllergenProfile }

func (p *profileRepoStub) Create(context.Context, *model.AllergenProfile, repository.AuditContext) error {
	return nil
}
func (p *profileRepoStub) Get(_ context.Context, id uint) (model.AllergenProfile, error) {
	return p.profiles[id], nil
}
func (p *profileRepoStub) List(context.Context, dto.ProfileQuery) ([]model.AllergenProfile, int64, error) {
	return nil, 0, nil
}
func (p *profileRepoStub) Update(context.Context, *model.AllergenProfile, uint, repository.AuditContext) error {
	return nil
}
func (p *profileRepoStub) Usage(context.Context, uint) ([]dto.ProfileUsage, error) { return nil, nil }
func (p *profileRepoStub) GetMany(_ context.Context, ids []uint) ([]model.AllergenProfile, error) {
	result := make([]model.AllergenProfile, 0, len(ids))
	for _, id := range ids {
		if profile, ok := p.profiles[id]; ok {
			result = append(result, profile)
		}
	}
	return result, nil
}

type edgeRepoStub struct{ edges []model.ContactEdge }

func (e *edgeRepoStub) Create(context.Context, *model.ContactEdge, repository.AuditContext) error {
	return nil
}
func (e *edgeRepoStub) Get(context.Context, uint) (model.ContactEdge, error) {
	return model.ContactEdge{}, nil
}
func (e *edgeRepoStub) List(context.Context, dto.ContactEdgeQuery) ([]model.ContactEdge, int64, error) {
	return nil, 0, nil
}
func (e *edgeRepoStub) ForRoute(context.Context, uint) ([]model.ContactEdge, error) { return e.edges, nil }
func (e *edgeRepoStub) Update(context.Context, *model.ContactEdge, uint, repository.AuditContext) error {
	return nil
}

type assessmentFixture struct {
	service *AssessmentService
	runs    *runRepoStub
	routes  *routeRepoStub
}

func newAssessmentFixture(t *testing.T) *assessmentFixture {
	t.Helper()
	steps, err := json.Marshal([]dto.RouteStep{{StepCode: "MIX-01", StepName: "Mixing", ProfileID: 1}, {StepCode: "PACK-02", StepName: "Packing", ProfileID: 2}})
	if err != nil {
		t.Fatalf("marshal steps: %v", err)
	}
	declared, err := json.Marshal([]string{"Milk"})
	if err != nil {
		t.Fatalf("marshal declared: %v", err)
	}
	routes := &routeRepoStub{route: model.ProcessRoute{ID: 1, RouteCode: "RT-1", ProductName: "Line study", OrderedStepsJSON: datatypes.JSON(steps), DeclaredAllergensJSON: datatypes.JSON(declared), RouteStatus: "active", Version: 1}}
	profiles := &profileRepoStub{profiles: map[uint]model.AllergenProfile{
		1: {ID: 1, ProfileCode: "P1", MaterialName: "Peanut paste", AllergensJSON: datatypes.JSON([]byte(`["Peanut"]`)), ProfileStatus: "active", Version: 1},
		2: {ID: 2, ProfileCode: "P2", MaterialName: "Milk powder", AllergensJSON: datatypes.JSON([]byte(`["Milk"]`)), ProfileStatus: "active", Version: 1},
	}}
	edges := &edgeRepoStub{edges: []model.ContactEdge{{ID: 1, RouteID: 1, FromStepCode: "MIX-01", ToStepCode: "PACK-02", ContactType: "shared_line", SharedEquipment: "Filler", CleaningFactor: 0.4, CarryoverProbability: 0.8, EvidenceNote: "note", Enabled: true, Version: 1}}}
	runs := &runRepoStub{runs: map[uint]*model.AssessmentRun{}}
	cfg := config.Config{MaxPropagationDepth: 12, Thresholds: config.Thresholds{Medium: 0.12, High: 0.35, Critical: 0.65, Version: "2026.1"}}
	service, err := NewAssessmentService(runs, routes, profiles, edges, cfg)
	if err != nil {
		t.Fatalf("new assessment service: %v", err)
	}
	return &assessmentFixture{service: service, runs: runs, routes: routes}
}

func (f *assessmentFixture) addRun(t *testing.T, status constants.AssessmentStatus, snapshot map[string]any) model.AssessmentRun {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	f.runs.nextID++
	run := &model.AssessmentRun{ID: f.runs.nextID, RouteID: 1, AssessmentStatus: status, InputSnapshotJSON: datatypes.JSON(encoded), MatrixJSON: datatypes.JSON([]byte("[]")), RiskItemsJSON: datatypes.JSON([]byte("[]")), HighestRiskLevel: constants.RiskLow, AlgorithmVersion: "weighted-path-v1/2026.1", CreatedBy: 1}
	f.runs.runs[run.ID] = run
	return *run
}

func completeSnapshot() map[string]any {
	return map[string]any{
		"route":         map[string]any{"id": 1, "code": "RT-1", "version": 1, "steps": []dto.RouteStep{{StepCode: "MIX-01", StepName: "Mixing", ProfileID: 1}, {StepCode: "PACK-02", StepName: "Packing", ProfileID: 2}}, "declared_allergens": []string{"Milk"}},
		"profiles":      []map[string]any{{"id": 1, "code": "P1", "version": 1}, {"id": 2, "code": "P2", "version": 1}},
		"contact_edges": []map[string]any{{"id": 1, "version": 1, "enabled": true}},
	}
}

func diffKinds(items []dto.StaleDiffItem) map[string]dto.StaleDiffItem {
	result := make(map[string]dto.StaleDiffItem, len(items))
	for _, item := range items {
		result[item.Kind+"/"+item.Code+"/"+item.Change] = item
	}
	return result
}

func TestStaleDiffListsOnlyChangedInputs(t *testing.T) {
	fixture := newAssessmentFixture(t)
	run := fixture.addRun(t, constants.AssessmentStale, completeSnapshot())

	// Only profile P1 and a brand-new edge changed; route, P2 and edge 1 did not.
	profiles := fixture.service.profiles.(*profileRepoStub)
	profile := profiles.profiles[1]
	profile.Version = 2
	profiles.profiles[1] = profile
	edges := fixture.service.edges.(*edgeRepoStub)
	edges.edges = append(edges.edges, model.ContactEdge{ID: 2, RouteID: 1, FromStepCode: "PACK-02", ToStepCode: "MIX-01", ContactType: "sequence", SharedEquipment: "Belt", CleaningFactor: 0.2, CarryoverProbability: 0.5, EvidenceNote: "note", Enabled: true, Version: 1})

	diff, err := fixture.service.StaleDiff(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("stale diff: %v", err)
	}
	if diff.CurrentResultUsable {
		t.Fatal("diff marked usable despite changed inputs")
	}
	if len(diff.Items) != 2 {
		t.Fatalf("diff items = %d, want exactly the 2 changed inputs: %+v", len(diff.Items), diff.Items)
	}
	byKey := diffKinds(diff.Items)
	if _, ok := byKey["profile/P1/version_changed"]; !ok {
		t.Fatalf("missing profile version change: %+v", diff.Items)
	}
	if _, ok := byKey["contact_edge/PACK-02→MIX-01/added"]; !ok {
		t.Fatalf("missing added edge: %+v", diff.Items)
	}
}

func TestStaleDiffNoChangesMeansResultUsable(t *testing.T) {
	fixture := newAssessmentFixture(t)
	run := fixture.addRun(t, constants.AssessmentStale, completeSnapshot())
	diff, err := fixture.service.StaleDiff(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("stale diff: %v", err)
	}
	if len(diff.Items) != 0 {
		t.Fatalf("diff items = %+v, want none", diff.Items)
	}
	if !diff.CurrentResultUsable {
		t.Fatal("unchanged inputs should keep the existing result usable")
	}
}

func TestStaleDiffRouteStepChange(t *testing.T) {
	fixture := newAssessmentFixture(t)
	run := fixture.addRun(t, constants.AssessmentStale, completeSnapshot())
	steps, _ := json.Marshal([]dto.RouteStep{{StepCode: "MIX-01", StepName: "Mixing", ProfileID: 1}, {StepCode: "PACK-02", StepName: "Packing", ProfileID: 2}, {StepCode: "SEAL-03", StepName: "Sealing", ProfileID: 2}})
	fixture.routes.route.OrderedStepsJSON = datatypes.JSON(steps)
	fixture.routes.route.Version = 2
	diff, err := fixture.service.StaleDiff(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("stale diff: %v", err)
	}
	byKey := diffKinds(diff.Items)
	if _, ok := byKey["route/RT-1/steps_changed"]; !ok {
		t.Fatalf("missing route steps change: %+v", diff.Items)
	}
	if _, ok := byKey["route/RT-1/version_changed"]; ok {
		t.Fatalf("version change should not be reported alongside the concrete step change: %+v", diff.Items)
	}
}

func TestStaleDiffRequiresCompletedSnapshot(t *testing.T) {
	fixture := newAssessmentFixture(t)
	run := fixture.addRun(t, constants.AssessmentQueued, map[string]any{"route_id": 1, "route_version_at_queue": 1})
	if _, err := fixture.service.StaleDiff(context.Background(), run.ID); err == nil {
		t.Fatal("queued run without completed snapshot unexpectedly diffable")
	}
}

func TestRecalculateCreatesSupersedingPendingReviewRun(t *testing.T) {
	fixture := newAssessmentFixture(t)
	run := fixture.addRun(t, constants.AssessmentStale, completeSnapshot())
	profiles := fixture.service.profiles.(*profileRepoStub)
	profile := profiles.profiles[1]
	profile.Version = 2
	profiles.profiles[1] = profile

	actor := Principal{ID: 9, DisplayName: "质量分析员", Role: constants.RoleQualityAnalyst}
	created, err := fixture.service.Recalculate(context.Background(), run.ID, actor, "req-1")
	if err != nil {
		t.Fatalf("recalculate: %v", err)
	}
	if created.ID == run.ID {
		t.Fatal("recalculation must produce a new assessment run")
	}
	if created.AssessmentStatus != constants.AssessmentPendingReview {
		t.Fatalf("new run status = %s, want pending_review", created.AssessmentStatus)
	}
	if created.RecalcOfID == nil || *created.RecalcOfID != run.ID {
		t.Fatalf("new run recalc_of = %v, want %d", created.RecalcOfID, run.ID)
	}
	if created.CompletedAt == nil || len(created.RiskItemsJSON) == 0 || string(created.RiskItemsJSON) == "[]" {
		t.Fatal("recalculated run should carry freshly computed results")
	}
	original := fixture.runs.runs[run.ID]
	if original.SupersededByID == nil || *original.SupersededByID != created.ID {
		t.Fatalf("original superseded_by = %v, want %d", original.SupersededByID, created.ID)
	}
	if original.AssessmentStatus != constants.AssessmentStale {
		t.Fatalf("original status = %s, want it to stay stale (read-only)", original.AssessmentStatus)
	}
	if len(fixture.runs.diff) != 1 || fixture.runs.diff[0].Kind != "profile" {
		t.Fatalf("recalculation audit diff = %+v, want the profile change", fixture.runs.diff)
	}
}

func TestRecalculateGuardsState(t *testing.T) {
	fixture := newAssessmentFixture(t)
	actor := Principal{ID: 9, DisplayName: "质量分析员", Role: constants.RoleQualityAnalyst}

	pending := fixture.addRun(t, constants.AssessmentPendingReview, completeSnapshot())
	if _, err := fixture.service.Recalculate(context.Background(), pending.ID, actor, "req-1"); err == nil {
		t.Fatal("pending_review run unexpectedly recalculable")
	}

	rejected := fixture.addRun(t, constants.AssessmentRejected, completeSnapshot())
	created, err := fixture.service.Recalculate(context.Background(), rejected.ID, actor, "req-2")
	if err != nil {
		t.Fatalf("rejected run should be recalculable: %v", err)
	}
	if _, err := fixture.service.Recalculate(context.Background(), rejected.ID, actor, "req-3"); err == nil {
		t.Fatalf("run already superseded by #%d must not recalculate again", created.ID)
	}

	fixture.routes.route.RouteStatus = "retired"
	stale := fixture.addRun(t, constants.AssessmentStale, completeSnapshot())
	if _, err := fixture.service.Recalculate(context.Background(), stale.ID, actor, "req-4"); err == nil {
		t.Fatal("retired route unexpectedly recalculable")
	}
}
