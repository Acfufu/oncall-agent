package workspace

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSummaryRecordedHistoryHasNullableMissingMeasurements(t *testing.T) {
	d, err := Open(t.TempDir() + "/facts.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := Service{DB: d}
	empty, err := s.Summary(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty["citation_coverage"] != nil || empty["mean_diagnosis_ms"] != nil {
		t.Fatal("empty measures must remain null")
	}
	i := Incident{ID: "recorded", LifecycleStatus: "active", FirstReceivedAt: "2026-10-04T02:00:00Z"}
	data, err := s.Summary(context.Background(), []Incident{i})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		History []struct {
			Timestamp string   `json:"timestamp"`
			Received  int      `json:"received_incidents"`
			Mean      *float64 `json:"mean_diagnosis_ms"`
		} `json:"history"`
	}
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.History) != 1 || got.History[0].Received != 1 || got.History[0].Mean != nil || got.History[0].Timestamp != "2026-10-04T00:00:00Z" {
		t.Fatalf("invented history or measures: %s", b)
	}
}
