package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueRecoverEventStageAttemptCountsExecution(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	run, _, err := s.DB.Admit(ctx, observations(), true)
	if err != nil {
		t.Fatal(err)
	}
	first, ok, err := s.claim(ctx, run.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	second, ok, err := s.claim(ctx, run.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if first.CurrentAttempt != 1 || second.CurrentAttempt != 3 {
		t.Fatalf("fixture did not exercise fencing epochs %d %d", first.CurrentAttempt, second.CurrentAttempt)
	}
	events, err := s.DB.Events(ctx, run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	started := []Event{}
	for _, e := range events {
		if e.Stage == "queue" && e.Kind == "started" {
			started = append(started, e)
		}
	}
	if len(started) != 2 || started[0].StageAttempt != 1 || started[1].StageAttempt != 2 || started[1].Attempt != 3 {
		t.Fatalf("execution attempts conflated with fencing epochs %+v", started)
	}
	t.Log("real SQL Recover/claim: queue StageAttempt 1→2, fencing Attempt 1→3")
}
func TestNotificationFailedThenSuccessStageAttemptTwo(t *testing.T) {
	s, _ := fixtureService(t)
	ctx := context.Background()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	s.WebhookURL = srv.URL
	run, _, err := s.DB.Admit(ctx, observations(), true)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := s.claim(ctx, run.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = s.finish(ctx, claimed, time.Now(), "failed", "source_unavailable", SafeReport, nil, &APIError{Code: "fixture", Message: "fixture failure", Retryable: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessNotification(ctx, run.ID, []byte(`{"id":"fixture"}`)); err == nil {
		t.Fatal("failed real HTTP source claimed success")
	}
	if err = s.ProcessNotification(ctx, run.ID, []byte(`{"id":"fixture"}`)); err != nil {
		t.Fatal(err)
	}
	current, err := s.DB.Run(ctx, run.ID)
	if err != nil || current.NotificationStatus != "sent" || calls.Load() != 2 {
		t.Fatalf("delivery %+v %v calls=%d", current, err, calls.Load())
	}
	events, err := s.DB.Events(ctx, run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	terminal := []Event{}
	for _, e := range events {
		if e.Stage == "notification" && (e.Kind == "failed" || e.Kind == "succeeded") {
			terminal = append(terminal, e)
		}
	}
	if len(terminal) != 2 || terminal[0].StageAttempt != 1 || terminal[1].StageAttempt != 2 {
		t.Fatalf("notification retries not counted %+v", terminal)
	}
	t.Log("L1 actual notification HTTP503→200: events failed StageAttempt1 then succeeded StageAttempt2")
}
