package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type TopologyNode struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	Namespace   string `json:"namespace"`
	Service     string `json:"service"`
	SourceRef   string `json:"source_ref"`
}
type TopologyEdge struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	Target    string  `json:"target"`
	Kind      string  `json:"kind"`
	SourceRef string  `json:"source_ref"`
	ValidFrom string  `json:"valid_from"`
	ValidTo   *string `json:"valid_to"`
}
type PotentialImpact struct {
	ServiceID  string   `json:"service_id"`
	Label      string   `json:"label"`
	Basis      string   `json:"basis"`
	SourceRefs []string `json:"source_refs"`
}
type TopologySnapshot struct {
	Capability         string            `json:"capability"`
	Version            string            `json:"version"`
	ContentSHA256      string            `json:"content_sha256"`
	SourceRef          string            `json:"source_ref"`
	ValidFrom          string            `json:"valid_from"`
	ValidTo            *string           `json:"valid_to"`
	AsOf               string            `json:"as_of"`
	FocusID            string            `json:"focus_id"`
	Nodes              []TopologyNode    `json:"nodes"`
	Edges              []TopologyEdge    `json:"edges"`
	PotentialImpact    []PotentialImpact `json:"potential_impact"`
	HistoricalEvidence bool              `json:"historical_evidence"`
	ValidityState      string            `json:"validity_state"`
}
type Topology struct {
	snapshot  TopologySnapshot
	raw       []byte
	persisted bool
}

// ServiceTopologyID includes the environment and namespace and never uses display labels as identity.
func ServiceTopologyID(environment, namespace, service string) string {
	return url.PathEscape(environment) + "/" + url.PathEscape(namespace) + "/" + url.PathEscape(service)
}
func LoadTopology(path string) (*Topology, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("topology configuration unavailable")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, fmt.Errorf("topology configuration exceeds 1MiB")
	}
	var cfg struct {
		Version   string         `json:"version"`
		SourceRef string         `json:"source_ref"`
		ValidFrom string         `json:"valid_from"`
		ValidTo   *string        `json:"valid_to"`
		Nodes     []TopologyNode `json:"nodes"`
		Edges     []TopologyEdge `json:"edges"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid topology JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("invalid topology trailing data")
	}
	if strings.TrimSpace(cfg.Version) == "" || strings.TrimSpace(cfg.SourceRef) == "" || len(cfg.Nodes) > 300 || len(cfg.Edges) > 600 {
		return nil, fmt.Errorf("topology version/source/size invalid")
	}
	start, err := time.Parse(time.RFC3339Nano, cfg.ValidFrom)
	if err != nil {
		return nil, fmt.Errorf("topology valid_from invalid")
	}
	cfg.ValidFrom = start.UTC().Format(time.RFC3339Nano)
	if cfg.ValidTo != nil {
		end, e := time.Parse(time.RFC3339Nano, *cfg.ValidTo)
		if e != nil || end.Before(start) {
			return nil, fmt.Errorf("topology valid_to invalid")
		}
		val := end.UTC().Format(time.RFC3339Nano)
		cfg.ValidTo = &val
	}
	seen := map[string]TopologyNode{}
	for i, n := range cfg.Nodes {
		if strings.TrimSpace(n.Environment) == "" || strings.TrimSpace(n.Namespace) == "" || strings.TrimSpace(n.Service) == "" || strings.TrimSpace(n.SourceRef) == "" || len(n.Environment) > 128 || len(n.Namespace) > 128 || len(n.Service) > 256 {
			return nil, fmt.Errorf("topology node identity/source invalid")
		}
		id := ServiceTopologyID(n.Environment, n.Namespace, n.Service)
		if n.ID != "" && n.ID != id {
			return nil, fmt.Errorf("topology node ID does not match namespace")
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("duplicate topology service")
		}
		n.ID = id
		cfg.Nodes[i] = n
		seen[id] = n
	}
	edgeIDs := map[string]bool{}
	for i, e := range cfg.Edges {
		source, ok1 := seen[e.Source]
		target, ok2 := seen[e.Target]
		if !ok1 || !ok2 || source.Environment != target.Environment {
			return nil, fmt.Errorf("topology edge unknown service or cross environment")
		}
		if e.Kind == "" {
			e.Kind = "declared"
		}
		if (e.Kind != "declared" && e.Kind != "observed") || strings.TrimSpace(e.SourceRef) == "" {
			return nil, fmt.Errorf("topology edge kind/source invalid")
		}
		if e.ValidFrom == "" {
			e.ValidFrom = cfg.ValidFrom
		}
		from, err := time.Parse(time.RFC3339Nano, e.ValidFrom)
		if err != nil || from.Before(start) {
			return nil, fmt.Errorf("topology edge valid_from invalid")
		}
		e.ValidFrom = from.UTC().Format(time.RFC3339Nano)
		if e.ValidTo == nil {
			e.ValidTo = cfg.ValidTo
		}
		if e.ValidTo != nil {
			to, err := time.Parse(time.RFC3339Nano, *e.ValidTo)
			if err != nil || to.Before(from) {
				return nil, fmt.Errorf("topology edge valid_to invalid")
			}
			if cfg.ValidTo != nil {
				configTo, _ := time.Parse(time.RFC3339Nano, *cfg.ValidTo)
				if to.After(configTo) {
					return nil, fmt.Errorf("topology edge exceeds configuration validity")
				}
			}
			value := to.UTC().Format(time.RFC3339Nano)
			e.ValidTo = &value
		}
		e.ID = stableID("dep_", e.Source+":"+e.Target+":"+e.Kind+":"+e.SourceRef)
		if edgeIDs[e.ID] {
			return nil, fmt.Errorf("duplicate topology edge")
		}
		edgeIDs[e.ID] = true
		cfg.Edges[i] = e
	}
	sum := sha256.Sum256(raw)
	s := TopologySnapshot{Capability: "available", Version: cfg.Version, ContentSHA256: hex.EncodeToString(sum[:]), SourceRef: cfg.SourceRef, ValidFrom: cfg.ValidFrom, ValidTo: cfg.ValidTo, AsOf: stamp(), Nodes: cfg.Nodes, Edges: cfg.Edges, PotentialImpact: []PotentialImpact{}, HistoricalEvidence: false}
	if s.Nodes == nil {
		s.Nodes = []TopologyNode{}
	}
	if s.Edges == nil {
		s.Edges = []TopologyEdge{}
	}
	return &Topology{snapshot: s, raw: append([]byte(nil), raw...)}, nil
}

// PersistTopology stores an immutable controlled configuration version, never overwriting historical snapshots.
func (s *Service) PersistTopology(ctx context.Context, t *Topology) error {
	if t.persisted {
		return nil
	}
	var original struct {
		SourceRef string            `json:"source_ref"`
		Nodes     []json.RawMessage `json:"nodes"`
		Edges     []json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal(t.raw, &original); err != nil {
		return fmt.Errorf("invalid topology source snapshot")
	}
	fragments := map[string][]string{original.SourceRef: {string(t.raw)}}
	for _, entry := range append(original.Nodes, original.Edges...) {
		var v struct {
			SourceRef string `json:"source_ref"`
		}
		if err := json.Unmarshal(entry, &v); err != nil {
			return err
		}
		if v.SourceRef != original.SourceRef {
			fragments[v.SourceRef] = append(fragments[v.SourceRef], string(entry))
		}
	}
	sources := map[string]string{}
	for ref := range fragments {
		sources[ref] = stableID("topology_src_", t.snapshot.ContentSHA256+":"+ref)
	}
	next := t.snapshot
	next.Nodes = append([]TopologyNode(nil), t.snapshot.Nodes...)
	next.Edges = append([]TopologyEdge(nil), t.snapshot.Edges...)
	next.SourceRef = sources[t.snapshot.SourceRef]
	for i := range next.Nodes {
		next.Nodes[i].SourceRef = sources[next.Nodes[i].SourceRef]
	}
	for i := range next.Edges {
		next.Edges[i].SourceRef = sources[next.Edges[i].SourceRef]
	}
	tx, err := s.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS topology_versions(content_sha256 TEXT PRIMARY KEY,version TEXT NOT NULL,data TEXT NOT NULL)"); err != nil {
		return err
	}
	for ref, parts := range fragments {
		snippet := strings.Join(parts, "\n")
		if len(snippet) > 1<<20 {
			return fmt.Errorf("topology source snapshot exceeds 1MiB")
		}
		ev := Evidence{ID: sources[ref], Kind: "topology_definition", SourceKind: "manual", SourceRef: ref, RecordedAt: stamp(), Snippet: ptr(snippet), Metadata: map[string]any{"version": next.Version, "content_sha256": next.ContentSHA256, "valid_from": next.ValidFrom, "valid_to": next.ValidTo, "verification": "declared", "historical_evidence": false}}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO evidence(id,run_id,incident_id,data) VALUES(?,NULL,NULL,?)", ev.ID, marshal(ev)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO topology_versions VALUES(?,?,?)", next.ContentSHA256, next.Version, marshal(next)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	t.snapshot = next
	t.persisted = true
	return nil
}

// ForService traverses declared/observed links with visited sets; cycles never imply an actual outage.
func (t *Topology) ForService(ctx context.Context, environment, service string) (TopologySnapshot, error) {
	g := t.snapshot
	now := time.Now().UTC()
	g.AsOf = now.Format(time.RFC3339Nano)
	g.ValidityState = "active"
	if err := ctx.Err(); err != nil {
		return g, err
	}
	configStart, _ := time.Parse(time.RFC3339Nano, g.ValidFrom)
	if now.Before(configStart) {
		g.ValidityState = "inactive"
	}
	if g.ValidTo != nil {
		to, _ := time.Parse(time.RFC3339Nano, *g.ValidTo)
		if !now.Before(to) {
			g.ValidityState = "inactive"
		}
	}
	activeEdges := []TopologyEdge{}
	for _, edge := range t.snapshot.Edges {
		from, _ := time.Parse(time.RFC3339Nano, edge.ValidFrom)
		active := g.ValidityState == "active" && !now.Before(from)
		if edge.ValidTo != nil {
			to, _ := time.Parse(time.RFC3339Nano, *edge.ValidTo)
			active = active && now.Before(to)
		}
		if active {
			activeEdges = append(activeEdges, edge)
		}
	}
	g.Nodes = []TopologyNode{}
	g.Edges = []TopologyEdge{}
	g.PotentialImpact = []PotentialImpact{}
	focus := ""
	for _, n := range t.snapshot.Nodes {
		if n.Environment == environment && (n.Service == service || n.Namespace+"/"+n.Service == service || n.ID == service) {
			if focus != "" {
				return g, fmt.Errorf("ambiguous service namespace")
			}
			focus = n.ID
		}
	}
	if focus == "" {
		return g, fmt.Errorf("service not declared in selected environment")
	}
	g.FocusID = focus
	visible := map[string]bool{focus: true}
	potential := map[string][]string{}
	queue := []string{focus}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return g, err
		}
		id := queue[0]
		queue = queue[1:]
		for _, e := range activeEdges {
			if e.Source == id && !visible[e.Target] {
				visible[e.Target] = true
				queue = append(queue, e.Target)
			}
			if e.Target == id && !visible[e.Source] {
				visible[e.Source] = true
				queue = append(queue, e.Source)
			}
		}
	}
	reverse := []string{focus}
	visited := map[string]bool{focus: true}
	for len(reverse) > 0 {
		if err := ctx.Err(); err != nil {
			return g, err
		}
		id := reverse[0]
		reverse = reverse[1:]
		for _, e := range activeEdges {
			if e.Target == id && !visited[e.Source] {
				visited[e.Source] = true
				potential[e.Source] = append(append([]string(nil), potential[id]...), e.SourceRef)
				reverse = append(reverse, e.Source)
			}
		}
	}
	for _, n := range t.snapshot.Nodes {
		if visible[n.ID] {
			g.Nodes = append(g.Nodes, n)
		}
	}
	for _, e := range activeEdges {
		if visible[e.Source] && visible[e.Target] {
			g.Edges = append(g.Edges, e)
		}
	}
	for id, refs := range potential {
		g.PotentialImpact = append(g.PotentialImpact, PotentialImpact{ServiceID: id, Label: "潜在影响", Basis: "inferred", SourceRefs: refs})
	}
	sort.Slice(g.PotentialImpact, func(i, j int) bool { return g.PotentialImpact[i].ServiceID < g.PotentialImpact[j].ServiceID })
	return g, nil
}
