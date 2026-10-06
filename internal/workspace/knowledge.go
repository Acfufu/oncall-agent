package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"oncall-agent/internal/rag"
)

// RestoreProjection reconstructs BM25 and the active version filter solely from SQL.
func (s *Service) RestoreProjection(ctx context.Context) error {
	docs, err := s.DB.Documents(ctx)
	if err != nil {
		return err
	}
	active := []rag.ActiveVersion{}
	chunks := []rag.VersionChunk{}
	for _, d := range docs {
		if d.DeletedAt != nil || d.ActiveVersionID == nil {
			continue
		}
		v, err := s.DB.Version(ctx, *d.ActiveVersionID)
		if err != nil {
			return err
		}
		if v.Status != "active" || v.SpaceID != s.SpaceID {
			continue
		}
		active = append(active, rag.ActiveVersion{DocID: d.ID, VersionID: v.ID, SpaceID: v.SpaceID})
		for _, c := range v.Chunks {
			chunks = append(chunks, rag.VersionChunk{ID: c.ID, DocID: d.ID, VersionID: v.ID, Title: c.Title, Snippet: c.Snippet, Source: c.Source, SpaceID: c.SpaceID, Environment: d.Environment})
		}
	}
	s.RAG.RestoreChunks(chunks)
	s.RAG.ActivateVersions(active)
	return nil
}
func (s *Service) PutDocument(ctx context.Context, id, title, content, source, environment, expected, key string) (Document, Version, error) {
	s.knowledgeMu.Lock()
	defer s.knowledgeMu.Unlock()
	creating := id == ""
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 512 || strings.TrimSpace(content) == "" || len(content) > 512*1024 {
		return Document{}, Version{}, fmt.Errorf("title/content invalid or exceeds 512KiB")
	}
	if source != "upload" && source != "demo" {
		return Document{}, Version{}, fmt.Errorf("invalid source_kind")
	}
	if environment == "" {
		environment = "unknown"
	}
	namespace := "local:" + source
	if id == "" {
		id = stableID("doc_", namespace+":"+strings.ToLower(title))
	}
	bodyHash := hash(marshal([]string{id, title, content, source, environment}))
	scope := "document:" + id
	if creating {
		scope = "document:create"
	}
	if key != "" {
		var h, vid string
		err := s.DB.SQL.QueryRowContext(ctx, "SELECT body_hash,result_id FROM idempotency WHERE scope=? AND key=?", scope, key).Scan(&h, &vid)
		if err == nil {
			if h != bodyHash {
				return Document{}, Version{}, ErrConflict
			}
			d, e := s.DB.Document(ctx, id)
			if e != nil {
				return d, Version{}, e
			}
			v, e := s.DB.Version(ctx, vid)
			if e != nil {
				return d, v, e
			}
			if (v.Status == "active" || v.Status == "superseded") && v.IndexStatus == "indexed" {
				return d, v, s.restorePublishedProjection(ctx)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Document{}, Version{}, err
		}
	}
	d, err := s.DB.Document(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, Version{}, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		if expected != "" {
			return d, Version{}, ErrConflict
		}
		d = Document{ID: id, Title: title, SourceKind: source, Environment: environment, IndexStatus: "pending"}
		_, err = s.DB.SQL.ExecContext(ctx, "INSERT INTO documents VALUES(?,?,?,?)", id, namespace, strings.ToLower(title), marshal(d))
		if err != nil {
			return d, Version{}, err
		}
	} else {
		if d.SourceKind != source {
			return d, Version{}, ErrConflict
		}
		if expected != "" && (d.ActiveVersionID == nil || expected != *d.ActiveVersionID) {
			return d, Version{}, ErrConflict
		}
		if expected == "" && d.ActiveVersionID != nil && id != "" { // identical retransmission allowed, replacement needs compare-and-swap
			old, e := s.DB.Version(ctx, *d.ActiveVersionID)
			if e != nil {
				return d, Version{}, e
			}
			if old.ContentSHA256 != hash(content) || d.Title != title || d.SourceKind != source || d.Environment != environment {
				return d, Version{}, ErrConflict
			}
		}
	}
	if d.ActiveVersionID != nil {
		old, e := s.DB.Version(ctx, *d.ActiveVersionID)
		if e != nil {
			return d, Version{}, e
		}
		if old.ContentSHA256 == hash(content) && d.DeletedAt == nil && d.Title == title && d.Environment == environment {
			if key != "" {
				if _, e = s.DB.SQL.ExecContext(ctx, "INSERT OR IGNORE INTO idempotency VALUES(?,?,?,?)", scope, key, bodyHash, old.ID); e != nil {
					return d, old, e
				}
			}
			return d, old, s.restorePublishedProjection(ctx)
		}
	}
	vid := stableID("ver_", id+":"+hash(content)+":"+hash(marshal([]string{title, environment}))+":"+s.SpaceID)
	v := Version{ID: vid, DocumentID: id, Content: content, ContentSHA256: hash(content), CreatedAt: stamp(), Status: "staging", IndexStatus: "pending", SpaceID: s.SpaceID, Chunks: []Chunk{}}
	if prior, pe := s.DB.Version(ctx, vid); pe == nil {
		// Re-activation may change lifecycle, never the original content/chunks/creation timestamp.
		v = prior
		v.Status = "staging"
		v.IndexStatus = "pending"
	} else if !errors.Is(pe, sql.ErrNoRows) {
		return d, v, pe
	}

	_, err = s.DB.SQL.ExecContext(ctx, "INSERT INTO document_versions VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", v.ID, id, v.ContentSHA256, marshal(v))
	if err != nil {
		return d, v, err
	}
	if key != "" {
		if _, err = s.DB.SQL.ExecContext(ctx, "INSERT OR IGNORE INTO idempotency VALUES(?,?,?,?)", scope, key, bodyHash, vid); err != nil {
			return d, v, err
		}
	}
	if s.RAG == nil || (s.SourceReady != nil && !s.SourceReady(ctx)) {
		return d, v, ErrUnavailable
	}
	cs, err := s.RAG.IndexVersion(ctx, id, vid, title, content, source, s.SpaceID)
	if err != nil {
		v.IndexStatus = "failed"
		_, saveErr := s.DB.SQL.ExecContext(context.WithoutCancel(ctx), "UPDATE document_versions SET data=? WHERE id=?", marshal(v), vid)
		if saveErr != nil {
			return d, v, saveErr
		}
		return d, v, fmt.Errorf("index unavailable: %w", err)
	}
	v.Chunks = []Chunk{}
	for _, c := range cs {
		v.Chunks = append(v.Chunks, Chunk{ID: c.ID, VersionID: vid, DocumentID: id, Title: title, Snippet: c.Snippet, Source: source, SpaceID: s.SpaceID})
	}
	tx, err := s.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return d, v, err
	}
	defer tx.Rollback()
	if d.ActiveVersionID != nil {
		old, e := decodeRow[Version](tx.QueryRowContext(ctx, "SELECT data FROM document_versions WHERE id=?", *d.ActiveVersionID))
		if e != nil {
			return d, v, e
		}
		old.Status = "superseded"
		if _, e = tx.ExecContext(ctx, "UPDATE document_versions SET data=? WHERE id=?", marshal(old), old.ID); e != nil {
			return d, v, e
		}
	}
	v.Status = "active"
	v.IndexStatus = "indexed"
	d.ActiveVersionID = ptr(vid)
	d.DeletedAt = nil
	d.IndexStatus = "indexed"
	d.Title = title
	d.Environment = environment
	if _, err = tx.ExecContext(ctx, "UPDATE document_versions SET data=? WHERE id=?", marshal(v), vid); err != nil {
		return d, v, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE documents SET title=?,data=? WHERE id=?", strings.ToLower(title), marshal(d), id); err != nil {
		return d, v, err
	}
	for _, c := range v.Chunks {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO chunks VALUES(?,?,?)", c.ID, vid, marshal(c)); err != nil {
			return d, v, err
		}
	}
	if key != "" {
		if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO idempotency VALUES(?,?,?,?)", scope, key, bodyHash, vid); err != nil {
			return d, v, err
		}
	}
	if err = tx.Commit(); err != nil {
		return d, v, err
	}
	return d, v, s.restorePublishedProjection(ctx)
}
func (s *Service) DeleteDocument(ctx context.Context, id, expected string) (Document, error) {
	s.knowledgeMu.Lock()
	defer s.knowledgeMu.Unlock()
	d, err := s.DB.Document(ctx, id)
	if err != nil {
		return d, err
	}
	if d.ActiveVersionID == nil || expected == "" || expected != *d.ActiveVersionID {
		return d, ErrConflict
	}
	d.DeletedAt = ptr(stamp())
	d.IndexStatus = "cleanup_pending"
	tx, err := s.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE documents SET data=? WHERE id=?", marshal(d), id); err != nil {
		return d, err
	}
	cleanupID := stableID("cleanup_", id+":"+expected+":"+*d.DeletedAt)
	payload := []byte(marshal(cleanupPayload{DocumentID: id, VersionID: expected}))
	if _, err = tx.ExecContext(ctx, "INSERT INTO outbox VALUES(?,?,?,?,'pending',0,?,NULL)", cleanupID, "cleanup", cleanupID, payload, stamp()); err != nil {
		return d, err
	}
	if err = tx.Commit(); err != nil {
		return d, err
	}
	// Current retrieval is excluded immediately; physical projection cleanup is durable and retryable.
	return d, s.restorePublishedProjection(ctx)
}
func (s *Service) ReindexDemo(ctx context.Context, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	seen := map[string]bool{}
	for _, e := range entries {
		if err = ctx.Err(); err != nil {
			return count, err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, e2 := os.ReadFile(filepath.Join(dir, e.Name()))
		if e2 != nil {
			return count, e2
		}
		title := strings.TrimSuffix(e.Name(), ".md")
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(line[2:])
				break
			}
		}
		id := stableID("doc_", "local:demo:"+strings.ToLower(title))
		expected := ""
		if d, e2 := s.DB.Document(ctx, id); e2 == nil && d.ActiveVersionID != nil {
			expected = *d.ActiveVersionID
		}
		d, _, e2 := s.PutDocument(ctx, id, title, string(b), "demo", "unknown", expected, "")
		if e2 != nil {
			return count, e2
		}
		seen[d.ID] = true
		count++
	}
	docs, err := s.DB.Documents(ctx)
	if err != nil {
		return count, err
	}
	for _, d := range docs {
		if d.SourceKind == "demo" && d.DeletedAt == nil && d.ActiveVersionID != nil && !seen[d.ID] {
			if _, err = s.DeleteDocument(ctx, d.ID, *d.ActiveVersionID); err != nil {
				return count, err
			}
		}
	}
	return count, nil
}

// Once SQL publication commits, client cancellation cannot strand the retrieval projection.
func (s *Service) restorePublishedProjection(ctx context.Context) error {
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.RestoreProjection(recovery)
}

type cleanupPayload struct {
	DocumentID string `json:"doc_id"`
	VersionID  string `json:"version_id"`
}

// Serialize cleanup against publication so a late job cannot delete a reactivated version.
func (s *Service) dispatchCleanup(ctx context.Context, o Outbox) error {
	s.knowledgeMu.Lock()
	defer s.knowledgeMu.Unlock()
	var p cleanupPayload
	if err := json.Unmarshal(o.Payload, &p); err != nil {
		return err
	}
	if p.DocumentID == "" || p.VersionID == "" {
		return fmt.Errorf("invalid cleanup payload")
	}
	d, err := s.DB.Document(ctx, p.DocumentID)
	if err != nil {
		return err
	}
	active := d.DeletedAt == nil && d.ActiveVersionID != nil && *d.ActiveVersionID == p.VersionID
	if !active {
		if s.RAG == nil {
			return ErrUnavailable
		}
		if err = s.RAG.DeleteVersionWithContext(ctx, p.VersionID); err != nil {
			return err
		}
	}
	// A committed cleanup stays durable even if the originating request was cancelled.
	completion, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	tx, err := s.DB.SQL.BeginTx(completion, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !active && d.DeletedAt != nil && d.ActiveVersionID != nil && *d.ActiveVersionID == p.VersionID {
		d.IndexStatus = "removed"
		if _, err = tx.ExecContext(completion, "UPDATE documents SET data=? WHERE id=?", marshal(d), d.ID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(completion, "UPDATE outbox SET state='sent',attempts=attempts+1,last_error=NULL WHERE id=?", o.ID); err != nil {
		return err
	}
	return tx.Commit()
}
