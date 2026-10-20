// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"fmt"
	"strings"
)

// Audit trail (p_0.46.10).
//
// Nothing recorded who approved a design, changed a requirement, sealed a
// generation or deleted a milestone. For a planning record that a team hands to
// agents, "who decided this" is the first question asked about a plan, and it was
// unanswerable.
//
// The record is append-only: a correction is a new row, never an edit. It is
// written by ONE middleware over the mutating routes rather than by each handler,
// because a handler that forgets is exactly how an audit trail becomes a
// half-truth.

// AuditEntry is one attributed mutation.
type AuditEntry struct {
	ID         int64  `json:"id"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	EntityKind string `json:"entity_kind"`
	EntityID   string `json:"entity_id,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Detail     string `json:"detail,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	Ts         string `json:"ts"`
}

// auditKeepPerEntity bounds the history kept for one entity. A long-lived
// requirement edited daily for a year must not make its own detail page unusable.
const auditKeepPerEntity = 400

func (s *Store) ensureAuditTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS delivery_audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL DEFAULT '',
  entity_kind TEXT NOT NULL DEFAULT '',
  entity_id TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  request_id TEXT NOT NULL DEFAULT '',
  ts TEXT NOT NULL
)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS delivery_audit_entity
  ON delivery_audit(entity_kind, entity_id, id DESC)`)
	return err
}

// RecordAudit appends one entry and prunes that entity's oldest rows.
func (s *Store) RecordAudit(e AuditEntry) error {
	if strings.TrimSpace(e.EntityKind) == "" {
		return fmt.Errorf("entity_kind is required")
	}
	if e.Ts == "" {
		e.Ts = nowRFC3339()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
INSERT INTO delivery_audit(actor, action, entity_kind, entity_id, summary, detail, request_id, ts)
VALUES(?,?,?,?,?,?,?,?)`,
		e.Actor, e.Action, e.EntityKind, e.EntityID, truncateRunes(e.Summary, 400),
		truncateRunes(e.Detail, 4000), e.RequestID, e.Ts,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`
DELETE FROM delivery_audit
WHERE entity_kind=? AND entity_id=? AND id NOT IN (
  SELECT id FROM delivery_audit
  WHERE entity_kind=? AND entity_id=? ORDER BY id DESC LIMIT ?
)`, e.EntityKind, e.EntityID, e.EntityKind, e.EntityID, auditKeepPerEntity); err != nil {
		return err
	}
	return tx.Commit()
}

// ListAudit returns one entity's history, newest first.
func (s *Store) ListAudit(entityKind, entityID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT id, actor, action, entity_kind, entity_id, summary, detail, request_id, ts
FROM delivery_audit WHERE entity_kind=? AND entity_id=? ORDER BY id DESC LIMIT ?`,
		entityKind, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.EntityKind, &e.EntityID,
			&e.Summary, &e.Detail, &e.RequestID, &e.Ts); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddTrashProvenance records who deleted a major and when, so a restore can state
// what it is bringing back. Kept as a column pair rather than an audit lookup
// because the trash surface shows it inline for every row.
func (s *Store) ensureTrashProvenance() error {
	for _, q := range []string{
		`ALTER TABLE delivery_trash ADD COLUMN deleted_by TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_trash ADD COLUMN deleted_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE delivery_trash ADD COLUMN summary TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "duplicate column") {
				continue
			}
			return err
		}
	}
	return nil
}

// SetTrashProvenance stamps one trash row with who deleted it and when.
//
// It is keyed by the trash row id rather than by major: delivery_trash stores the
// entity as a JSON payload, so the major is not a column and matching on it would
// mean parsing every row to find one. The caller already has the id it just
// created.
func (s *Store) SetTrashProvenance(id, actor, summary string) error {
	_, err := s.db.Exec(
		`UPDATE delivery_trash SET deleted_by=?, deleted_at=?, summary=? WHERE id=?`,
		actor, nowRFC3339(), truncateRunes(summary, 600), id)
	return err
}

// TrashProvenance is what the trash dialog shows for one row.
func (s *Store) TrashProvenance(id string) (deletedBy, deletedAt, summary string, err error) {
	err = s.db.QueryRow(
		`SELECT COALESCE(deleted_by,''), COALESCE(deleted_at,''), COALESCE(summary,'')
		 FROM delivery_trash WHERE id=?`, id,
	).Scan(&deletedBy, &deletedAt, &summary)
	return
}

// The actor comes from the auth middleware (userFrom in server.go), which puts
// the authenticated user on the request context. Nothing here re-derives it: an
// audit row that guesses its own actor is worse than no row.
