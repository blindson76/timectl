package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"timectl/pkg/config"
)

func TestLoadPersistedStatusReadsAppliedFlag(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	manualTime := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 30, 12, 5, 0, 0, time.UTC)

	want := persistedStatus{
		Mode:                  config.ModeManual.String(),
		ManualTime:            &manualTime,
		OrderID:               42,
		LastUpdated:           now,
		ExternalTimeAvailable: true,
		Applied:               true,
	}

	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal persisted status: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dataDir, "last-status.json"), b, 0600); err != nil {
		t.Fatalf("write persisted status: %v", err)
	}

	s := &Server{cfg: &config.ServerConfig{DataDir: dataDir}}
	got, err := s.loadPersistedStatus()
	if err != nil {
		t.Fatalf("load persisted status: %v", err)
	}

	if got.Applied != want.Applied {
		t.Fatalf("applied = %v, want %v", got.Applied, want.Applied)
	}
	if got.Mode != want.Mode {
		t.Fatalf("mode = %q, want %q", got.Mode, want.Mode)
	}
	if got.OrderID != want.OrderID {
		t.Fatalf("orderID = %d, want %d", got.OrderID, want.OrderID)
	}
	if got.ManualTime == nil || !got.ManualTime.Equal(manualTime) {
		t.Fatalf("manualTime = %v, want %v", got.ManualTime, manualTime)
	}
}

func TestShouldApplyDecisionSkipsAlreadyAppliedState(t *testing.T) {
	t.Parallel()

	manualTime := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	s := &Server{
		hasAppliedDecision:    true,
		lastAppliedOrderID:    42,
		lastAppliedMode:       config.ModeManual,
		lastAppliedManualTime:  &manualTime,
	}

	state := config.TimeModeState{
		Mode:       config.ModeManual,
		OrderID:    42,
		ManualTime: &manualTime,
	}

	if s.shouldApplyDecision(state) {
		t.Fatal("expected already-applied state to be skipped")
	}

	state.OrderID = 43
	if !s.shouldApplyDecision(state) {
		t.Fatal("expected new order to be applied")
	}
}