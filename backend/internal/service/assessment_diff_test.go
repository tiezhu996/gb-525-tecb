package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"food-allergen-crosscontact-analyzer/backend/internal/config"
	"food-allergen-crosscontact-analyzer/backend/internal/constants"
	"food-allergen-crosscontact-analyzer/backend/internal/dto"
	"food-allergen-crosscontact-analyzer/backend/internal/model"
	"food-allergen-crosscontact-analyzer/backend/internal/repository"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var diffDBCounter atomic.Uint64

type diffFixture struct {
	db       *gorm.DB
	svc      *AssessmentService
	route    model.ProcessRoute
	profile1 model.AllergenProfile
	profile2 model.AllergenProfile
	edge     model.ContactEdge
	actor    Principal
}

func newDiffFixture(t *testing.T) diffFixture {
	t.Helper()
	dsn := fmt.Sprintf("file:assessment_diff_%d?mode=memory&cache=shared", diffDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.AllergenProfile{}, &model.ProcessRoute{}, &model.ContactEdge{}, &model.AssessmentRun{}, &model.AuditEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	profilesRepo := repository.NewProfileRepository(db)
	routesRepo := repository.NewRouteRepository(db)
	edgesRepo := repository.NewContactEdgeRepository(db)
	runsRepo := repository.NewAssessmentRepository(db)
	cfg := config.Config{MaxPropagationDepth: 8, Thresholds: config.Thresholds{Medium: 0.12, High: 0.35, Critical: 0.65, Version: "test.1"}}
	svc, err := NewAssessmentService(runsRepo, routesRepo, profilesRepo, edgesRepo, cfg)
	if err != nil {
		t.Fatalf("new assessment service: %v", err)
	}
	actor := Principal{ID: 1, Username: "analyst", DisplayName: "Analyst", Role: constants.RoleQualityAnalyst}

	peanutJSON, _ := json.Marshal([]string{"Peanut"})
	milkJSON, _ := json.Marshal([]string{"Milk"})
	profile1 := model.AllergenProfile{ProfileCode: "MAT-PEANUT", MaterialName: "Peanut paste", AllergensJSON: datatypes.JSON(peanutJSON), SourceType: "supplier_statement", ProfileStatus: "active", Version: 1, CreatedBy: actor.ID}
	profile2 := model.AllergenProfile{ProfileCode: "MAT-MILK", MaterialName: "Milk powder", AllergensJSON: datatypes.JSON(milkJSON), SourceType: "supplier_statement", ProfileStatus: "active", Version: 1, CreatedBy: actor.ID}
	if err := db.Create(&profile1).Error; err != nil {
		t.Fatalf("create profile1: %v", err)
	}
	if err := db.Create(&profile2).Error; err != nil {
		t.Fatalf("create profile2: %v", err)
	}
	steps, _ := json.Marshal([]dto.RouteStep{{StepCode: "MIX-01", StepName: "Mixing", ProfileID: profile1.ID}, {StepCode: "FILL-02", StepName: "Filling", ProfileID: profile2.ID}})
	declared, _ := json.Marshal([]string{"Milk"})
	route := model.ProcessRoute{RouteCode: "RT-DIFF", ProductName: "Diff study", OrderedStepsJSON: datatypes.JSON(steps), DeclaredAllergensJSON: datatypes.JSON(declared), RouteStatus: "active", Version: 1, OwnerID: actor.ID}
	if err := db.Create(&route).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	edge := model.ContactEdge{RouteID: route.ID, FromStepCode: "MIX-01", ToStepCode: "FILL-02", ContactType: "shared_line", SharedEquipment: "Filler A", CleaningFactor: 0.4, CarryoverProbability: 0.8, EvidenceNote: "validated cleaning", Enabled: true, Version: 1, CreatedBy: actor.ID}
	if err := db.Create(&edge).Error; err != nil {
		t.Fatalf("create edge: %v", err)
	}
	return diffFixture{db: db, svc: svc, route: route, profile1: profile1, profile2: profile2, edge: edge, actor: actor}
}

// createCompletedRun 排队、运行并返回已完成、进入待复核的评估。
func (f diffFixture) createCompletedRun(t *testing.T) model.AssessmentRun {
	t.Helper()
	ctx := context.Background()
	queued, err := f.svc.Create(ctx, dto.CreateAssessmentRequest{RouteID: f.route.ID}, f.actor, "req-queue")
	if err != nil {
		t.Fatalf("create assessment: %v", err)
	}
	if _, err := f.svc.Run(ctx, queued.ID, f.actor, "req-run"); err != nil {
		t.Fatalf("run assessment: %v", err)
	}
	run, err := f.svc.Get(ctx, queued.ID)
	if err != nil {
		t.Fatalf("reload assessment: %v", err)
	}
	if run.AssessmentStatus != constants.AssessmentPendingReview {
		t.Fatalf("status = %s, want pending_review", run.AssessmentStatus)
	}
	return run
}

func (f diffFixture) setStatus(t *testing.T, id uint, status constants.AssessmentStatus) {
	t.Helper()
	if err := f.db.Model(&model.AssessmentRun{}).Where("id = ?", id).Update("assessment_status", status).Error; err != nil {
		t.Fatalf("set status %s: %v", status, err)
	}
}

func (f diffFixture) bumpProfileVersion(t *testing.T, id uint) {
	t.Helper()
	if err := f.db.Model(&model.AllergenProfile{}).Where("id = ?", id).UpdateColumn("version", gorm.Expr("version + 1")).Error; err != nil {
		t.Fatalf("bump profile version: %v", err)
	}
}

func (f diffFixture) bumpEdgeVersion(t *testing.T, id uint, enabled bool) {
	t.Helper()
	if err := f.db.Model(&model.ContactEdge{}).Where("id = ?", id).Updates(map[string]any{"version": gorm.Expr("version + 1"), "enabled": enabled}).Error; err != nil {
		t.Fatalf("bump edge version: %v", err)
	}
}

func TestDiffEmptyWhenInputsUnchanged(t *testing.T) {
	f := newDiffFixture(t)
	run := f.createCompletedRun(t)
	f.setStatus(t, run.ID, constants.AssessmentStale)

	diff, err := f.svc.Diff(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if diff.HasChanges {
		t.Fatalf("HasChanges = true, want false; changes: %+v", diff)
	}
	if diff.RecomputeAvailable {
		t.Fatal("RecomputeAvailable = true, want false when snapshot still matches")
	}
	if _, err := f.svc.Recompute(context.Background(), run.ID, f.actor, "req-recompute"); err == nil {
		t.Fatal("recompute without changes unexpectedly allowed")
	}
}

func TestDiffAttributesOnlyChangedProfile(t *testing.T) {
	f := newDiffFixture(t)
	run := f.createCompletedRun(t)
	f.setStatus(t, run.ID, constants.AssessmentStale)
	f.bumpProfileVersion(t, f.profile1.ID)

	diff, err := f.svc.Diff(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !diff.HasChanges || !diff.RecomputeAvailable {
		t.Fatalf("expected changed profile to enable recompute: %+v", diff)
	}
	if len(diff.ProfileChanges) != 1 {
		t.Fatalf("profile changes = %d, want 1", len(diff.ProfileChanges))
	}
	change := diff.ProfileChanges[0]
	if change.ProfileID != f.profile1.ID || change.SnapshotVersion != 1 || change.CurrentVersion != 2 || change.ChangeType != "version_changed" {
		t.Fatalf("unexpected profile change: %+v", change)
	}
	if len(diff.RouteStepChanges) != 0 || len(diff.ContactEdgeChanges) != 0 {
		t.Fatalf("expected only the profile change, got steps=%+v edges=%+v", diff.RouteStepChanges, diff.ContactEdgeChanges)
	}
}

func TestDiffAttributesChangedEdge(t *testing.T) {
	f := newDiffFixture(t)
	run := f.createCompletedRun(t)
	f.setStatus(t, run.ID, constants.AssessmentStale)
	f.bumpEdgeVersion(t, f.edge.ID, false)

	diff, err := f.svc.Diff(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(diff.ContactEdgeChanges) != 1 {
		t.Fatalf("edge changes = %d, want 1", len(diff.ContactEdgeChanges))
	}
	edgeChange := diff.ContactEdgeChanges[0]
	if edgeChange.EdgeID != f.edge.ID || edgeChange.ChangeType != "version_changed" {
		t.Fatalf("unexpected edge change: %+v", edgeChange)
	}
	if edgeChange.SnapshotEnabled == nil || *edgeChange.SnapshotEnabled != true || edgeChange.CurrentEnabled == nil || *edgeChange.CurrentEnabled != false {
		t.Fatalf("unexpected enabled flags: %+v", edgeChange)
	}
}

func TestDiffRejectsNonStaleOrRejected(t *testing.T) {
	f := newDiffFixture(t)
	run := f.createCompletedRun(t)
	if _, err := f.svc.Diff(context.Background(), run.ID); err == nil {
		t.Fatal("diff on pending_review run unexpectedly allowed")
	}
}

func TestRecomputeSupersedesSourceAndAudits(t *testing.T) {
	f := newDiffFixture(t)
	run := f.createCompletedRun(t)
	f.setStatus(t, run.ID, constants.AssessmentStale)
	f.bumpProfileVersion(t, f.profile1.ID)

	newRun, err := f.svc.Recompute(context.Background(), run.ID, f.actor, "req-recompute")
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if newRun.ID == run.ID {
		t.Fatal("recompute must create a new assessment record")
	}
	if newRun.AssessmentStatus != constants.AssessmentPendingReview {
		t.Fatalf("new run status = %s, want pending_review", newRun.AssessmentStatus)
	}
	source, err := f.svc.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if source.AssessmentStatus != constants.AssessmentStale {
		t.Fatalf("source status = %s, want stale preserved", source.AssessmentStatus)
	}
	if source.SupersededByID == nil || *source.SupersededByID != newRun.ID {
		t.Fatalf("source superseded_by = %v, want %d", source.SupersededByID, newRun.ID)
	}

	var events []model.AuditEvent
	if err := f.db.Where("entity_type = ?", "assessment_run").Order("id ASC").Find(&events).Error; err != nil {
		t.Fatalf("load audits: %v", err)
	}
	var sawSuperseded, sawRecomputed bool
	for _, event := range events {
		switch event.Action {
		case "assessment.superseded":
			sawSuperseded = true
			if event.EntityID != run.ID {
				t.Fatalf("superseded audit entity = %d, want %d", event.EntityID, run.ID)
			}
			if !json.Valid(event.MetadataJSON) {
				t.Fatal("superseded audit metadata is not valid JSON")
			}
			if !containsDiffMetadata(event.MetadataJSON) {
				t.Fatalf("superseded audit metadata missing diff: %s", event.MetadataJSON)
			}
		case "assessment.recomputed":
			sawRecomputed = true
			if event.EntityID != newRun.ID {
				t.Fatalf("recomputed audit entity = %d, want %d", event.EntityID, newRun.ID)
			}
			if !containsDiffMetadata(event.MetadataJSON) {
				t.Fatalf("recomputed audit metadata missing diff: %s", event.MetadataJSON)
			}
		}
	}
	if !sawSuperseded || !sawRecomputed {
		t.Fatalf("expected superseded=%v recomputed=%v audits", sawSuperseded, sawRecomputed)
	}

	// 已被替代的原记录不能再次重算。
	if _, err := f.svc.Recompute(context.Background(), run.ID, f.actor, "req-again"); err == nil {
		t.Fatal("recompute on already superseded run unexpectedly allowed")
	}
}

func TestDiffAttributesStepAndDeclaredChanges(t *testing.T) {
	f := newDiffFixture(t)
	run := f.createCompletedRun(t)
	f.setStatus(t, run.ID, constants.AssessmentStale)

	// 路线步骤换成另一张谱，并调整声明过敏原。
	steps, _ := json.Marshal([]dto.RouteStep{{StepCode: "MIX-01", StepName: "Mixing", ProfileID: f.profile2.ID}, {StepCode: "FILL-02", StepName: "Filling", ProfileID: f.profile2.ID}})
	declared, _ := json.Marshal([]string{"Milk", "Peanut"})
	if err := f.db.Model(&model.ProcessRoute{}).Where("id = ?", f.route.ID).Updates(map[string]any{"ordered_steps_json": datatypes.JSON(steps), "declared_allergens_json": datatypes.JSON(declared), "version": gorm.Expr("version + 1")}).Error; err != nil {
		t.Fatalf("update route: %v", err)
	}

	diff, err := f.svc.Diff(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !diff.HasChanges || !diff.RecomputeAvailable {
		t.Fatalf("expected route changes to enable recompute: %+v", diff)
	}
	if len(diff.RouteStepChanges) != 1 {
		t.Fatalf("step changes = %d, want 1", len(diff.RouteStepChanges))
	}
	stepChange := diff.RouteStepChanges[0]
	if stepChange.StepCode != "MIX-01" || stepChange.SnapshotProfileID != f.profile1.ID || stepChange.CurrentProfileID != f.profile2.ID {
		t.Fatalf("unexpected step change: %+v", stepChange)
	}
	// 谱本身没改版，不应计入谱变化；新引用的谱由步骤项体现。
	if len(diff.ProfileChanges) != 0 {
		t.Fatalf("profile changes = %+v, want none", diff.ProfileChanges)
	}
	if diff.RouteChange == nil || !diff.RouteChange.VersionChanged || len(diff.RouteChange.DeclaredChanges) != 1 {
		t.Fatalf("unexpected route change: %+v", diff.RouteChange)
	}
	if diff.RouteChange.DeclaredChanges[0].Allergen != "Peanut" || diff.RouteChange.DeclaredChanges[0].ChangeType != "added" {
		t.Fatalf("unexpected declared change: %+v", diff.RouteChange.DeclaredChanges)
	}
}

func TestRejectedRunAllowsRecompute(t *testing.T) {
	f := newDiffFixture(t)
	run := f.createCompletedRun(t)
	f.setStatus(t, run.ID, constants.AssessmentRejected)

	diff, err := f.svc.Diff(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("diff rejected: %v", err)
	}
	if !diff.RecomputeAvailable {
		t.Fatal("rejected run must offer recompute even without changes")
	}
	newRun, err := f.svc.Recompute(context.Background(), run.ID, f.actor, "req-recompute-rejected")
	if err != nil {
		t.Fatalf("recompute rejected: %v", err)
	}
	if newRun.AssessmentStatus != constants.AssessmentPendingReview {
		t.Fatalf("new run status = %s, want pending_review", newRun.AssessmentStatus)
	}
}

func containsDiffMetadata(raw datatypes.JSON) bool {
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return false
	}
	diff, ok := metadata["diff"].(map[string]any)
	if !ok {
		return false
	}
	_, hasChanges := diff["has_changes"]
	return hasChanges
}
