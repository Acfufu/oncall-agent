package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrConflict = errors.New("version or idempotency conflict")
var ErrUnavailable = errors.New("dependency unavailable")
var ErrFenced = errors.New("attempt no longer owns run")

func canonical(o Observation, now time.Time) (string, string, *string, error) {
	labels := map[string]string{}
	for k, v := range o.Labels {
		labels[k] = v
	}
	if labels["alertname"] == "" {
		labels["alertname"] = o.Name
	}
	if labels["alertname"] == "" {
		return "", "", nil, fmt.Errorf("alertname required")
	}
	quality := "exact"
	var start *string
	epoch := ""
	if o.StartsAt != "" {
		t, e := time.Parse(time.RFC3339Nano, o.StartsAt)
		if e != nil {
			return "", "", nil, fmt.Errorf("invalid startsAt")
		}
		epoch = t.UTC().Format(time.RFC3339Nano)
		start = ptr(epoch)
	} else {
		quality = "windowed"
		epoch = now.UTC().Truncate(5 * time.Minute).Format(time.RFC3339)
	}
	env := labels["environment"]
	if env == "" {
		env = "unknown"
	}
	return hash("alert-v1:alertmanager:" + env + ":" + marshal(labels) + ":" + epoch), quality, start, nil
}
func ParseObservations(raw []byte) ([]Observation, error) {
	var root struct {
		Status string `json:"status"`
		Alerts []struct {
			Status      string            `json:"status"`
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			StartsAt    string            `json:"startsAt"`
			EndsAt      string            `json:"endsAt"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("invalid alert JSON")
	}
	var out []Observation
	if root.Alerts != nil {
		for _, a := range root.Alerts {
			status := a.Status
			if status == "" {
				status = root.Status
			}
			if status == "" {
				status = "firing"
			}
			desc := a.Annotations["description"]
			if desc == "" {
				desc = a.Annotations["summary"]
			}
			out = append(out, Observation{Name: a.Labels["alertname"], Severity: a.Labels["severity"], Description: desc, Labels: a.Labels, Annotations: a.Annotations, StartsAt: a.StartsAt, EndsAt: a.EndsAt, Status: status})
		}
	} else {
		var one Observation
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, err
		}
		if one.Name == "" {
			one.Name = one.Labels["alertname"]
		}
		if one.Description == "" {
			one.Description = one.Annotations["description"]
		}
		if one.Name != "" {
			if one.Status == "" {
				one.Status = "firing"
			}
			out = append(out, one)
		} else {
			var list []Observation
			if err := json.Unmarshal(raw, &list); err == nil {
				out = list
			}
		}
	}
	if len(out) == 0 || len(out) > 100 {
		return nil, fmt.Errorf("alert batch must contain 1..100 observations")
	}
	for i := range out {
		if out[i].Status == "" {
			out[i].Status = "firing"
		}
		if out[i].Status != "firing" && out[i].Status != "resolved" {
			return nil, fmt.Errorf("invalid alert status")
		}
		if out[i].Labels == nil {
			out[i].Labels = map[string]string{}
		}
		if out[i].Labels["alertname"] == "" {
			out[i].Labels["alertname"] = out[i].Name
		}
		if out[i].Name == "" {
			out[i].Name = out[i].Labels["alertname"]
		}
		if out[i].Name == "" {
			return nil, fmt.Errorf("alertname required")
		}
	}
	return out, nil
}
func severity(s string) string {
	switch strings.ToLower(s) {
	case "p0", "critical":
		return "P0"
	case "p1", "warning":
		return "P1"
	case "p2":
		return "P2"
	case "p3", "info":
		return "P3"
	}
	return "unknown"
}

// Admit serializes canonical identity, immutable observations, run and outbox in one transaction.
// knownReady is evaluated before the first new automatic run; duplicates remain readable during outages.
func (d *DB) Admit(ctx context.Context, obs []Observation, knownReady bool) (*Run, bool, error) {
	if len(obs) == 0 || len(obs) > 100 {
		return nil, false, fmt.Errorf("invalid batch size")
	}
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	at := now.Format(time.RFC3339Nano)
	ids := []string{}
	for _, o := range obs {
		key, quality, start, e := canonical(o, now)
		if e != nil {
			return nil, false, e
		}
		id := stableID("inc_", key)
		inc, e := decodeRow[Incident](tx.QueryRowContext(ctx, "SELECT data FROM incidents WHERE id=?", id))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, false, e
		}
		if errors.Is(e, sql.ErrNoRows) {
			if o.Status == "resolved" { // unmatched recovery is audited, never creates a diagnosis or fictitious incident
				_, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO alert_observations VALUES(?,NULL,?,?)", stableID("obs_", key+marshal(o)), hash(key+marshal(o)), marshal(o))
				if e != nil {
					return nil, false, e
				}
				continue
			}
			env, svc := o.Labels["environment"], o.Labels["service"]
			if env == "" {
				env = "unknown"
			}
			if svc == "" {
				svc = "unknown"
			}
			inc = Incident{ID: id, SourceNamespace: "alertmanager", Environment: env, Service: svc, Name: o.Name, Severity: severity(o.Severity), LifecycleStatus: "active", StartsAt: start, FirstReceivedAt: at, LastObservedAt: at, IdentityQuality: quality}
			_, e = tx.ExecContext(ctx, "INSERT INTO incidents VALUES(?,?,?,?,?,?)", id, key, env, svc, at, marshal(inc))
			if e != nil {
				return nil, false, e
			}
		}
		if o.Status == "resolved" {
			if start == nil || o.EndsAt == "" {
				return nil, false, fmt.Errorf("resolved observation requires startsAt and endsAt")
			}
			end, e := time.Parse(time.RFC3339Nano, o.EndsAt)
			begin, _ := time.Parse(time.RFC3339Nano, *start)
			if e != nil || end.Before(begin) {
				return nil, false, fmt.Errorf("invalid endsAt")
			}
			val := end.UTC().Format(time.RFC3339Nano)
			if inc.ResolvedAt == nil || val > *inc.ResolvedAt {
				inc.ResolvedAt = ptr(val)
				inc.LifecycleStatus = "resolved"
			}
		} else {
			ids = append(ids, id)
		}
		inc.LastObservedAt = at
		_, e = tx.ExecContext(ctx, "UPDATE incidents SET data=? WHERE id=?", marshal(inc), id)
		if e != nil {
			return nil, false, e
		}
		observationID := stableID("obs_", key+marshal(o))
		_, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO alert_observations VALUES(?,?,?,?)", observationID, id, hash(key+marshal(o)), marshal(o))
		if e != nil {
			return nil, false, e
		}
	}
	sort.Strings(ids)
	ids = unique(ids)
	if len(ids) == 0 {
		return nil, false, tx.Commit()
	}
	business := hash("policy-v1:" + strings.Join(ids, ","))
	existing, e := decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE business_key=?", business))
	if e == nil {
		return &existing, true, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, false, e
	}
	if !knownReady {
		return nil, false, ErrUnavailable
	}
	run := Run{ID: stableID("run_", business), IncidentIDs: ids, Status: "queued", DispatchState: "pending", EvidenceStatus: "not_evaluated", NotificationStatus: "disabled", ReceivedAt: at, Revision: 1, CitationIDs: []string{}}
	if e = insertRun(ctx, tx, run, business, obs); e != nil {
		return nil, false, e
	}
	if e = tx.Commit(); e != nil {
		return nil, false, e
	}
	return &run, false, nil
}
func unique(a []string) []string {
	b := a[:0]
	for _, v := range a {
		if len(b) == 0 || b[len(b)-1] != v {
			b = append(b, v)
		}
	}
	return b
}
func insertRun(ctx context.Context, tx *sql.Tx, r Run, business string, obs []Observation) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO diagnosis_runs(id,business_key,status,received_at,data) VALUES(?,?,?,?,?)", r.ID, business, r.Status, r.ReceivedAt, marshal(r))
	if err != nil {
		return err
	}
	for _, id := range r.IncidentIDs {
		_, err = tx.ExecContext(ctx, "INSERT INTO run_incidents VALUES(?,?)", r.ID, id)
		if err != nil {
			return err
		}
		inc, e := decodeRow[Incident](tx.QueryRowContext(ctx, "SELECT data FROM incidents WHERE id=?", id))
		if e != nil {
			return e
		}
		inc.LatestRunID = ptr(r.ID)
		if _, e = tx.ExecContext(ctx, "UPDATE incidents SET data=? WHERE id=?", marshal(inc), id); e != nil {
			return e
		}
	}
	for _, id := range r.IncidentIDs {
		observations, e := listRows[Observation](ctx, tx, "SELECT data FROM alert_observations WHERE incident_id=? ORDER BY rowid DESC LIMIT 1", id)
		if e != nil {
			return e
		}
		if len(observations) == 0 {
			continue
		}
		ev := Evidence{ID: stableID("alert_", r.ID+id), Kind: "alert_observation", SourceKind: "manual", SourceRef: id, RecordedAt: r.ReceivedAt, ObservedAt: ptr(observations[0].StartsAt), Metadata: map[string]any{"observation": observations[0], "run_id": r.ID, "incident_id": id}}
		if _, e = tx.ExecContext(ctx, "INSERT INTO evidence VALUES(?,?,?,?)", ev.ID, r.ID, id, marshal(ev)); e != nil {
			return e
		}
	}
	for _, stage := range []string{"receive", "queue"} {
		if err = addEvent(ctx, tx, r, stage, "succeeded", nil, nil, nil); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO outbox VALUES(?,?,?,?,'pending',0,?,NULL)", "diagnosis:"+r.ID, "diagnosis", "diagnosis:"+r.ID, []byte(marshal(struct {
		RunID        string        `json:"run_id"`
		Observations []Observation `json:"observations"`
	}{r.ID, obs})), stamp())
	return err
}
func (d *DB) ReDiagnose(ctx context.Context, incidentID, key, body string, ready bool) (Run, error) {
	if key == "" || len(key) > 200 {
		return Run{}, fmt.Errorf("Idempotency-Key required")
	}
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	var oldHash, runID string
	err = tx.QueryRowContext(ctx, "SELECT body_hash,result_id FROM idempotency WHERE scope=? AND key=?", "run:"+incidentID, key).Scan(&oldHash, &runID)
	if err == nil {
		if oldHash != hash(body) {
			return Run{}, ErrConflict
		}
		r, e := decodeRow[Run](tx.QueryRowContext(ctx, "SELECT data FROM diagnosis_runs WHERE id=?", runID))
		return r, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Run{}, err
	}
	if !ready {
		return Run{}, ErrUnavailable
	}
	inc, err := decodeRow[Incident](tx.QueryRowContext(ctx, "SELECT data FROM incidents WHERE id=?", incidentID))
	if err != nil {
		return Run{}, err
	}
	obs, err := listRows[Observation](ctx, tx, "SELECT data FROM alert_observations WHERE incident_id=? ORDER BY rowid DESC LIMIT 1", incidentID)
	if err != nil || len(obs) == 0 {
		return Run{}, fmt.Errorf("observation unavailable")
	}
	obs[0].Status = "firing"
	business := hash("manual:" + incidentID + ":" + key)
	r := Run{ID: stableID("run_", business), IncidentIDs: []string{inc.ID}, Status: "queued", DispatchState: "pending", EvidenceStatus: "not_evaluated", NotificationStatus: "disabled", ReceivedAt: stamp(), Revision: 1, CitationIDs: []string{}}
	if err = insertRun(ctx, tx, r, business, obs); err != nil {
		return Run{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO idempotency VALUES(?,?,?,?)", "run:"+incidentID, key, hash(body), r.ID)
	if err != nil {
		return Run{}, err
	}
	return r, tx.Commit()
}
