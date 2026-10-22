package repro

import (
    "strings"
    "testing"
)

// PASS means the current undesirable behavior was reproduced, not that the
// original application's business acceptance criteria are satisfied.
func TestReproSameAlertDifferentReportIDDefeatsDedup(t *testing.T) {
    p1 := []byte(`{"report_id":"a","alerts":[{"name":"X"}]}`)
    p2 := []byte(`{"report_id":"b","alerts":[{"name":"X"}]}`)
    a, b := taskID(p1), taskID(p2)
    if a == b { t.Fatal("expected the currently defective differing IDs") }
    t.Logf("same alerts, report_id a/b -> %s / %s", a, b)
}

func TestReproNoCitationGuardKeepsUnsupportedInstruction(t *testing.T) {
    instruction := "把 worker 数量设置为 99。"
    reply := refusalGuard(instruction, 0)
    if !strings.Contains(reply, instruction) { t.Fatal("unsupported instruction was not preserved") }
    // Source: cmd/evalbaseline/main.go, genRefusal's final predicate.
    reportedAsRefusal := strings.Contains(reply, "未找到相关匹配")
    if !reportedAsRefusal { t.Fatal("expected evaluator's refusal predicate to pass") }
    t.Logf("unsupported instruction retained; eval refusal predicate = %v; reply=%q", reportedAsRefusal, reply)
}

func TestReproMismatchedVectorDimensionsCanScoreOne(t *testing.T) {
    similarity := cosine([]float32{1, 0}, []float32{1, 0, 1000})
    if similarity != 1 { t.Fatalf("got %v, expected current behavior 1", similarity) }
    t.Logf("dimensions 2 vs 3 accepted; similarity=%v", similarity)
}

func TestReproChangedSnippetCreatesNewIDInsteadOfReplacing(t *testing.T) {
    oldID := chunkID("runbook", 0, "old instruction")
    newID := chunkID("runbook", 0, "replacement instruction")
    if oldID == newID { t.Fatal("expected new ID for changed content") }
    t.Logf("same doc/chunk position, changed content -> %s / %s", oldID, newID)
    // This tests the identity formula only. The missing deletion in Upload/
    // AddDoc is independently established by static review, not by this test.
}
