package scheduler

import (
	"fmt"
	"sync"
	"testing"
)

const tol = 1e-9

func floatsEqual(a, b, tol float64) bool {
	if a == b {
		return true
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestRecordSuccess_EWMA(t *testing.T) {
	st := NewStatsTracker()
	key := BackendModelKey{BackendID: "b1", ModelName: "m1"}

	// First sample: stored directly (no prior EWMA value).
	st.RecordSuccess(key, 100.0, 0.5)

	if got := st.GetTokensPerSecond(key, 0); !floatsEqual(got, 100.0, tol) {
		t.Errorf("TPS after 1 sample: got %f, want %f", got, 100.0)
	}
	if got := st.GetColdLoadTime(key, 0); !floatsEqual(got, 0.5, tol) {
		t.Errorf("ColdLoad after 1 sample: got %f, want %f", got, 0.5)
	}

	// Second sample: EWMA applied.
	// TPS: 0.2*200 + 0.8*100 = 120
	// ColdLoad: 0.2*0.3 + 0.8*0.5 = 0.46
	st.RecordSuccess(key, 200.0, 0.3)

	if got := st.GetTokensPerSecond(key, 0); !floatsEqual(got, 120.0, tol) {
		t.Errorf("TPS after 2 samples: got %f, want %f", got, 120.0)
	}
	if got := st.GetColdLoadTime(key, 0); !floatsEqual(got, 0.46, tol) {
		t.Errorf("ColdLoad after 2 samples: got %f, want %f", got, 0.46)
	}

	// Third sample.
	// TPS: 0.2*300 + 0.8*120 = 156
	// ColdLoad: 0.2*0.2 + 0.8*0.46 = 0.408
	st.RecordSuccess(key, 300.0, 0.2)

	if got := st.GetTokensPerSecond(key, 0); !floatsEqual(got, 156.0, tol) {
		t.Errorf("TPS after 3 samples: got %f, want %f", got, 156.0)
	}
	if got := st.GetColdLoadTime(key, 0); !floatsEqual(got, 0.408, tol) {
		t.Errorf("ColdLoad after 3 samples: got %f, want %f", got, 0.408)
	}
}

func TestGetTokensPerSecond_Unknown(t *testing.T) {
	st := NewStatsTracker()
	key := BackendModelKey{BackendID: "unknown", ModelName: "m1"}

	got := st.GetTokensPerSecond(key, 42.0)
	if got != 42.0 {
		t.Errorf("got %f, want %f", got, 42.0)
	}
}

func TestGetTokensPerSecond_Known(t *testing.T) {
	st := NewStatsTracker()
	key := BackendModelKey{BackendID: "b1", ModelName: "m1"}

	st.RecordSuccess(key, 25.0, 0.0)

	got := st.GetTokensPerSecond(key, 999.0)
	if got != 25.0 {
		t.Errorf("got %f, want %f", got, 25.0)
	}
}

func TestGetColdLoadTime_Unknown(t *testing.T) {
	st := NewStatsTracker()
	key := BackendModelKey{BackendID: "unknown", ModelName: "m1"}

	got := st.GetColdLoadTime(key, 1.5)
	if got != 1.5 {
		t.Errorf("got %f, want %f", got, 1.5)
	}
}

func TestGetColdLoadTime_Known(t *testing.T) {
	st := NewStatsTracker()
	key := BackendModelKey{BackendID: "b1", ModelName: "m1"}

	st.RecordSuccess(key, 0.0, 1.5)

	got := st.GetColdLoadTime(key, 999.0)
	if got != 1.5 {
		t.Errorf("got %f, want %f", got, 1.5)
	}
}

func TestRecordFailure_Increments(t *testing.T) {
	st := NewStatsTracker()
	key := BackendModelKey{BackendID: "b1", ModelName: "m1"}

	st.RecordFailure(key)
	st.RecordFailure(key)
	st.RecordFailure(key)

	got := st.GetConsecutiveFailures(key)
	if got != 3 {
		t.Errorf("got %d, want %d", got, 3)
	}
}

func TestRecordSuccess_ResetsFailures(t *testing.T) {
	st := NewStatsTracker()
	key := BackendModelKey{BackendID: "b1", ModelName: "m1"}

	st.RecordFailure(key)
	st.RecordFailure(key)
	st.RecordSuccess(key, 10.0, 0.1)

	got := st.GetConsecutiveFailures(key)
	if got != 0 {
		t.Errorf("got %d, want %d", got, 0)
	}
}

func TestSnapshotAll(t *testing.T) {
	st := NewStatsTracker()
	key1 := BackendModelKey{BackendID: "b1", ModelName: "m1"}
	key2 := BackendModelKey{BackendID: "b2", ModelName: "m2"}

	st.RecordSuccess(key1, 100.0, 0.5)
	st.RecordSuccess(key2, 200.0, 1.0)

	snap := st.SnapshotAll()

	if len(snap) != 2 {
		t.Fatalf("snapshot has %d entries, want 2", len(snap))
	}

	v1, ok := snap[key1]
	if !ok {
		t.Errorf("key1 not found in snapshot")
	} else if v1.AvgTokensPerSecond != 100.0 {
		t.Errorf("key1 TPS: got %f, want %f", v1.AvgTokensPerSecond, 100.0)
	}

	v2, ok := snap[key2]
	if !ok {
		t.Errorf("key2 not found in snapshot")
	} else if v2.AvgTokensPerSecond != 200.0 {
		t.Errorf("key2 TPS: got %f, want %f", v2.AvgTokensPerSecond, 200.0)
	}

	// Verify snapshot is a deep copy: modifying the tracker should not
	// affect the previously returned snapshot.
	st.RecordSuccess(key1, 300.0, 0.3)
	snapAfter := st.SnapshotAll()

	if snap[key1].AvgTokensPerSecond != 100.0 {
		t.Errorf("old snapshot was mutated by tracker: got %f, want %f",
			snap[key1].AvgTokensPerSecond, 100.0)
	}
	if snapAfter[key1].AvgTokensPerSecond == snap[key1].AvgTokensPerSecond {
		t.Errorf("snapshot was not a copy: old and new snapshot share data")
	}
}

func TestPrune(t *testing.T) {
	st := NewStatsTracker()
	keepKey := BackendModelKey{BackendID: "b1", ModelName: "model-a:latest"}
	dropKey := BackendModelKey{BackendID: "b1", ModelName: "model-gone:latest"}
	otherBackend := BackendModelKey{BackendID: "b2", ModelName: "model-a:latest"}

	st.RecordSuccess(keepKey, 100.0, 0.5)
	st.RecordSuccess(dropKey, 200.0, 0.8)
	st.RecordSuccess(otherBackend, 300.0, 1.0)

	// Keep only rows matching b1/model-a:latest.
	keep := func(k BackendModelKey) bool {
		return k.BackendID == "b1" && k.ModelName == "model-a:latest"
	}
	st.Prune(keep)

	if got := st.GetSamples(keepKey); got != 1 {
		t.Errorf("kept key samples: got %d, want 1", got)
	}
	if got := st.GetSamples(dropKey); got != 0 {
		t.Errorf("dropped key samples: got %d, want 0 (row should be pruned)", got)
	}
	if got := st.GetSamples(otherBackend); got != 0 {
		t.Errorf("other-backend key samples: got %d, want 0 (row should be pruned)", got)
	}
	if got := len(st.SnapshotAll()); got != 1 {
		t.Errorf("snapshot after prune has %d entries, want 1", got)
	}
}

func TestConcurrentSafety(t *testing.T) {
	st := NewStatsTracker()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := BackendModelKey{
				BackendID: fmt.Sprintf("b%d", id),
				ModelName: "m1",
			}
			st.RecordSuccess(key, float64(id)*10, float64(id)*0.1)
			st.GetTokensPerSecond(key, 0)
			st.GetColdLoadTime(key, 0)
			st.GetConsecutiveFailures(key)
		}(i)
	}

	wg.Wait()

	snap := st.SnapshotAll()
	if len(snap) != 10 {
		t.Errorf("expected 10 entries in snapshot, got %d", len(snap))
	}
}
