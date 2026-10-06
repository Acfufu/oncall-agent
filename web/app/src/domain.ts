/** Proposed v1 read model. Not the full API or database schema. */
export type DataState = 'fresh' | 'stale' | 'partial' | 'unavailable';
export type IncidentStatus = 'active' | 'resolved' | 'unknown';
export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';
export type EvidenceStatus = 'not_evaluated' | 'has_citations' | 'no_evidence' | 'source_unavailable';
export type Verification = 'not_applicable' | 'unverified' | 'confirmed' | 'rejected';
export type Stage = 'receive' | 'queue' | 'retrieval' | 'report' | 'judge' | 'notification';
export type EventKind = 'started' | 'succeeded' | 'failed' | 'skipped' | 'cancelled';
export type Relation = 'contains' | 'triggered' | 'retrieved' | 'cites' | 'produced'
  | 'observed_on' | 'depends_on' | 'suggests' | 'possibly_supports';
export type NodeType = 'incident' | 'alert' | 'retrieval' | 'document_chunk'
  | 'report' | 'service' | 'metric_observation' | 'hypothesis';
export type SourceKind = 'demo' | 'upload' | 'incident' | 'prometheus' | 'manual' | 'system';
export type EvidenceKind = 'document_chunk' | 'retrieval' | 'report'
  | 'metric_observation' | 'alert_observation' | 'topology' | 'annotation';
export type ISODateTime = string;

export interface ApiError { code: string; message: string; retryable: boolean }
export interface ResponseMeta {
  request_id: string;
  as_of: ISODateTime;
  data_state: DataState;
  mode: 'live' | 'demo';
  revision: number;
  next_cursor: string | null;
  truncated: boolean;
}
export interface Incident {
  id: string;
  source_namespace: string;
  environment: string;
  service: string;
  name: string;
  severity: 'P0' | 'P1' | 'P2' | 'P3' | 'unknown';
  lifecycle_status: IncidentStatus;
  starts_at: ISODateTime | null;
  resolved_at: ISODateTime | null;
  first_received_at: ISODateTime;
  last_observed_at: ISODateTime;
  identity_quality: 'exact' | 'windowed';
  latest_run_id: string | null;
}
export interface DiagnosisRun {
  id: string;
  incident_ids: string[];
  status: RunStatus;
  dispatch_state: 'pending' | 'enqueued' | 'none';
  evidence_status: EvidenceStatus;
  notification_status: 'disabled' | 'pending' | 'sent' | 'failed';
  judge_score: number | null; // integer 1..5 when scored; NEVER a probability
  received_at: ISODateTime;
  started_at: ISODateTime | null;
  finished_at: ISODateTime | null;
  duration_ms: number | null;
  current_attempt: number;
  revision: number;
  report_text: string | null;
  citation_ids: string[]; // EvidenceRecord ids, bound to the current run
  error: ApiError | null;
}
export interface EvidenceRecord {
  id: string;
  kind: EvidenceKind;
  source_kind: SourceKind;
  source_ref: string; // opaque locator; NEVER an executable URL/command
  recorded_at: ISODateTime;
  observed_at: ISODateTime | null;
  document_id: string | null;
  version_id: string | null;
  chunk_id: string | null;
  snippet: string | null;
  metadata: Record<string, unknown>;
}
export interface GraphNode {
  id: string;
  type: NodeType;
  label: string;
  subtitle: string;
  entity_ref: string;
  source_refs: string[];
  verification: Verification;
}
export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  relation: Relation;
  basis: 'recorded' | 'declared' | 'observed' | 'inferred';
  source_refs: string[];
  recorded_at: ISODateTime;
  verification: Verification;
}
export interface GraphSnapshot {
  id: string;
  incident_id: string;
  run_id: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
  truncated: boolean;
  omitted_counts: { nodes: number; edges: number };
}
export interface RunEvent {
  event_id: string;
  run_id: string;
  attempt: number;
  sequence: number;
  stage: Stage;
  stage_attempt: number;
  kind: EventKind;
  occurred_at: ISODateTime;
  duration_ms: number | null;
  result_ref: string | null;
  error: ApiError | null;
}
export interface SelectedIncidentSummary {
  scope: 'selected_incident';
  active_incidents: number | null;
  no_evidence_incidents: number | null;
  succeeded_runs: number;
  cited_succeeded_runs: number;
  mean_diagnosis_ms: number | null;
}
/** Composite fixture/UI-view boundary; REST resources can be fetched separately. */
export interface WorkspaceSnapshot {
  schema_version: '1.0';
  meta: ResponseMeta;
  incident: Incident;
  run: DiagnosisRun;
  graph: GraphSnapshot;
  evidence: EvidenceRecord[];
  events: RunEvent[];
  summary: SelectedIncidentSummary;
}

/** Fixture-only labels are not fields required from the live REST API. */
export interface WorkspaceFixture extends WorkspaceSnapshot {
  fixture_id: string;
  fixture_phase: 'M1' | 'M2';
}
