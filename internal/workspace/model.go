// Package workspace coordinates durable business facts with existing read-only adapters.
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Incident struct {
	ID              string  `json:"id"`
	SourceNamespace string  `json:"source_namespace"`
	Environment     string  `json:"environment"`
	Service         string  `json:"service"`
	Name            string  `json:"name"`
	Severity        string  `json:"severity"`
	LifecycleStatus string  `json:"lifecycle_status"`
	StartsAt        *string `json:"starts_at"`
	ResolvedAt      *string `json:"resolved_at"`
	FirstReceivedAt string  `json:"first_received_at"`
	LastObservedAt  string  `json:"last_observed_at"`
	IdentityQuality string  `json:"identity_quality"`
	LatestRunID     *string `json:"latest_run_id"`
}
type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type Run struct {
	ID                 string    `json:"id"`
	IncidentIDs        []string  `json:"incident_ids"`
	Status             string    `json:"status"`
	DispatchState      string    `json:"dispatch_state"`
	EvidenceStatus     string    `json:"evidence_status"`
	NotificationStatus string    `json:"notification_status"`
	JudgeScore         *int      `json:"judge_score"`
	ReceivedAt         string    `json:"received_at"`
	StartedAt          *string   `json:"started_at"`
	FinishedAt         *string   `json:"finished_at"`
	DurationMS         *int64    `json:"duration_ms"`
	CurrentAttempt     int       `json:"current_attempt"`
	Revision           int       `json:"revision"`
	ReportText         *string   `json:"report_text"`
	CitationIDs        []string  `json:"citation_ids"`
	Error              *APIError `json:"error"`
}
type Event struct {
	EventID      string    `json:"event_id"`
	RunID        string    `json:"run_id"`
	Attempt      int       `json:"attempt"`
	Sequence     int       `json:"sequence"`
	Stage        string    `json:"stage"`
	StageAttempt int       `json:"stage_attempt"`
	Kind         string    `json:"kind"`
	OccurredAt   string    `json:"occurred_at"`
	DurationMS   *int64    `json:"duration_ms"`
	ResultRef    *string   `json:"result_ref"`
	Error        *APIError `json:"error"`
}
type Evidence struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	SourceKind string         `json:"source_kind"`
	SourceRef  string         `json:"source_ref"`
	RecordedAt string         `json:"recorded_at"`
	ObservedAt *string        `json:"observed_at"`
	DocumentID *string        `json:"document_id"`
	VersionID  *string        `json:"version_id"`
	ChunkID    *string        `json:"chunk_id"`
	Snippet    *string        `json:"snippet"`
	Metadata   map[string]any `json:"metadata"`
}
type Node struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Label        string   `json:"label"`
	Subtitle     string   `json:"subtitle"`
	EntityRef    string   `json:"entity_ref"`
	SourceRefs   []string `json:"source_refs"`
	Verification string   `json:"verification"`
}
type Edge struct {
	ID           string   `json:"id"`
	Source       string   `json:"source"`
	Target       string   `json:"target"`
	Relation     string   `json:"relation"`
	Basis        string   `json:"basis"`
	SourceRefs   []string `json:"source_refs"`
	RecordedAt   string   `json:"recorded_at"`
	Verification string   `json:"verification"`
}
type Graph struct {
	Revision      int            `json:"revision"`
	AsOf          string         `json:"as_of"`
	ID            string         `json:"id"`
	IncidentID    string         `json:"incident_id"`
	RunID         string         `json:"run_id"`
	Nodes         []Node         `json:"nodes"`
	Edges         []Edge         `json:"edges"`
	Truncated     bool           `json:"truncated"`
	OmittedCounts map[string]int `json:"omitted_counts"`
}
type Document struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	SourceKind      string  `json:"source_kind"`
	Environment     string  `json:"environment"`
	ActiveVersionID *string `json:"active_version_id"`
	DeletedAt       *string `json:"deleted_at"`
	IndexStatus     string  `json:"index_status"`
}
type Chunk struct {
	ID         string `json:"id"`
	VersionID  string `json:"version_id"`
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Snippet    string `json:"snippet"`
	Source     string `json:"source"`
	SpaceID    string `json:"space_id"`
}
type Version struct {
	ID            string  `json:"id"`
	DocumentID    string  `json:"document_id"`
	Content       string  `json:"content"`
	ContentSHA256 string  `json:"content_sha256"`
	CreatedAt     string  `json:"created_at"`
	Status        string  `json:"status"`
	IndexStatus   string  `json:"index_status"`
	SpaceID       string  `json:"embedding_space_id"`
	Chunks        []Chunk `json:"chunks"`
}
type Observation struct {
	Name        string            `json:"name"`
	Severity    string            `json:"severity"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	EndsAt      string            `json:"endsAt"`
	Status      string            `json:"status"`
}
type Outbox struct {
	ID       string
	Kind     string
	Payload  []byte
	Attempts int
}

func hash(s string) string             { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func stableID(prefix, s string) string { return prefix + hash(s)[:32] }
func stamp() string                    { return time.Now().UTC().Format(time.RFC3339Nano) }
func ptr(s string) *string             { return &s }
func marshal(v any) string             { b, _ := json.Marshal(v); return string(b) }

const SafeReport = "当前未找到可用于处置的合格知识，请人工研判。"
