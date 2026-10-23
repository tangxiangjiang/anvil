// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	searchSchemaVersion = 1
	searchMaxQueryRunes = 160
	searchMaxTerms      = 12
	searchMaxTermRunes  = 64
	searchMaxResults    = 50
)

var ErrSearchQueryTooLong = errors.New("search query is too long")

// SearchResult is a safe projection of one FTS row. It intentionally omits
// audit actors/request ids, job arguments, worker metadata and database scope
// keys: those fields are neither indexed nor returned.
type SearchResult struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
	Href    string `json:"href"`
	Scope   string `json:"scope,omitempty"`
}

// ensureSearchIndex installs an FTS5 index and source-table triggers. The first
// migration backfills existing rows once; later opens rely on the triggers and
// do not rescan the planning tables.
func (s *Store) ensureSearchIndex() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS delivery_search_meta (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  schema_version INTEGER NOT NULL
)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
CREATE VIRTUAL TABLE IF NOT EXISTS delivery_search USING fts5(
  kind UNINDEXED, entity_id UNINDEXED, title, body,
  href UNINDEXED, scope UNINDEXED, scope_key UNINDEXED, updated_at UNINDEXED,
  tokenize='unicode61 remove_diacritics 2'
)`); err != nil {
		return fmt.Errorf("delivery search requires SQLite FTS5: %w", err)
	}
	if _, err := tx.Exec(searchTriggerSQL); err != nil {
		return fmt.Errorf("install delivery search triggers: %w", err)
	}

	var version int
	err = tx.QueryRow(`SELECT schema_version FROM delivery_search_meta WHERE singleton=1`).Scan(&version)
	if err != nil || version != searchSchemaVersion {
		if _, err := tx.Exec(`DELETE FROM delivery_search`); err != nil {
			return err
		}
		if _, err := tx.Exec(searchBackfillSQL); err != nil {
			return fmt.Errorf("backfill delivery search: %w", err)
		}
		if _, err := tx.Exec(`
INSERT INTO delivery_search_meta(singleton, schema_version) VALUES(1,?)
ON CONFLICT(singleton) DO UPDATE SET schema_version=excluded.schema_version`,
			searchSchemaVersion); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Search performs a parameterized FTS MATCH. User input is converted to quoted
// Unicode letter/number tokens, so operators, columns, quotes and parentheses
// can never become FTS syntax. All terms must match.
func (s *Store) Search(query, scopeKey string, limit int) ([]SearchResult, error) {
	match, empty, err := safeFTSQuery(query)
	if err != nil {
		return nil, err
	}
	if empty {
		return []SearchResult{}, nil
	}
	if limit <= 0 || limit > searchMaxResults {
		limit = searchMaxResults
	}
	q := `
SELECT kind, entity_id, title,
       CASE
         WHEN instr(snippet(delivery_search, 3, '‹', '›', ' … ', 24), '‹') > 0
           THEN snippet(delivery_search, 3, '‹', '›', ' … ', 24)
         ELSE highlight(delivery_search, 2, '‹', '›')
       END,
       href, scope
FROM delivery_search
WHERE delivery_search MATCH ?`
	args := []any{match}
	if strings.TrimSpace(scopeKey) != "" {
		q += ` AND scope_key=?`
		args = append(args, strings.TrimSpace(scopeKey))
	}
	q += ` ORDER BY bm25(delivery_search, 0.0, 0.0, 8.0, 1.0), updated_at DESC, kind, entity_id LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("search planning record: %w", err)
	}
	defer rows.Close()
	out := []SearchResult{}
	for rows.Next() {
		var hit SearchResult
		if err := rows.Scan(&hit.Kind, &hit.ID, &hit.Title, &hit.Excerpt, &hit.Href, &hit.Scope); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func safeFTSQuery(raw string) (match string, empty bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true, nil
	}
	if utf8.RuneCountInString(raw) > searchMaxQueryRunes {
		return "", false, ErrSearchQueryTooLong
	}
	var terms []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 || len(terms) >= searchMaxTerms {
			b.Reset()
			return
		}
		terms = append(terms, `"`+b.String()+`"`)
		b.Reset()
	}
	n := 0
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			if n < searchMaxTermRunes {
				b.WriteRune(r)
			}
			n++
			continue
		}
		flush()
		n = 0
	}
	flush()
	if len(terms) == 0 {
		return "", true, nil
	}
	return strings.Join(terms, " AND "), false, nil
}

const searchBackfillSQL = `
INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'requirement_version',
       v.eng_req_id || ':' || v.major || '.' || v.minor || '.' || v.patch,
       v.title,
       v.problem || char(10) || v.goals || char(10) || v.non_goals || char(10) || v.constraints_text || char(10) || v.success,
       '#/delivery/requirements/' || v.eng_req_id || '/' || v.major || '/' || v.minor || '/' || v.patch,
       e.title,
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id),
       v.updated_at
FROM delivery_req_versions v JOIN delivery_eng_reqs e ON e.id=v.eng_req_id;

INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'design', d.id, d.title,
       d.adr || char(10) || d.adr_context || char(10) || d.adr_decision || char(10) ||
       d.adr_consequences || char(10) || d.adr_spec || char(10) || d.adr_architecture ||
       char(10) || d.adr_sequence || char(10) || d.adr_sample,
       '#/delivery/designs/' || d.id,
       e.title || ' · ' || d.major || '.' || d.minor,
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id),
       d.updated_at
FROM delivery_designs d JOIN delivery_eng_reqs e ON e.id=d.eng_req_id;

INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'design', v.id, COALESCE(NULLIF(v.title,''), d.title),
       v.gate || char(10) || v.sub_architecture || char(10) || v.class_design,
       '#/delivery/designs/' || d.id,
       e.title || ' · ' || d.major || '.' || d.minor,
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id),
       v.updated_at
FROM delivery_design_versions v
JOIN delivery_designs d ON d.id=v.design_id
JOIN delivery_eng_reqs e ON e.id=d.eng_req_id;

INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'milestone', m.id, m.title,
       m.acceptance || char(10) || m.test_note || char(10) || m.merge_note,
       '#/delivery/roadmap/m/' || COALESCE(v.major,0),
       COALESCE(e.title,''),
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id, ''),
       COALESCE(m.merged_at, m.accepted_at, '')
FROM delivery_milestones m
LEFT JOIN delivery_req_versions v
  ON m.id=printf('ms_%s_%d_%d', v.eng_req_id, v.major, v.minor) AND v.patch=1
LEFT JOIN delivery_eng_reqs e ON e.id=v.eng_req_id;

INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'test_case', c.id, c.title, c.body, '#/delivery/test',
       e.title || ' · ' || c.major || '.' || c.minor,
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id), c.created_at
FROM delivery_test_cases c JOIN delivery_eng_reqs e ON e.id=c.eng_req_id;

INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'bug', b.id, b.title, b.body, '#/delivery/test',
       e.title || ' · ' || b.major || '.' || b.minor,
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id), b.created_at
FROM delivery_buglist_items b JOIN delivery_eng_reqs e ON e.id=b.eng_req_id;

INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'job', j.job_id, COALESCE(NULLIF(j.kind,''),'Job') || ' ' || j.job_id,
       j.error, '#/jobs', COALESCE(e.title, j.worker_id),
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id, ''), j.started_at
FROM delivery_jobs j LEFT JOIN delivery_eng_reqs e ON e.id=j.eng_req_id;

INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'job_event', ev.job_id || ':' || ev.seq, COALESCE(NULLIF(ev.name,''), NULLIF(ev.type,''), 'Job event'),
       ev.text || char(10) || ev.content, '#/jobs', COALESCE(e.title, j.worker_id),
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id, ''), ev.ts
FROM delivery_job_events ev
JOIN delivery_jobs j ON j.job_id=ev.job_id
LEFT JOIN delivery_eng_reqs e ON e.id=j.eng_req_id;

INSERT INTO delivery_search(kind, entity_id, title, body, href, scope, scope_key, updated_at)
SELECT 'audit', CAST(a.id AS TEXT), a.summary, a.action || char(10) || a.detail,
       '#/delivery', a.entity_kind || ' · ' || a.entity_id,
       COALESCE(NULLIF(e.workspace_id,''), NULLIF(e.project_id,''), e.id, ''), a.ts
FROM delivery_audit a
LEFT JOIN delivery_eng_reqs e ON e.id=CASE
  WHEN a.entity_kind='requirement' THEN a.entity_id
  WHEN a.entity_kind='requirement_version' THEN substr(a.entity_id,1,instr(a.entity_id,':')-1)
  WHEN a.entity_kind='design' THEN (SELECT eng_req_id FROM delivery_designs WHERE id=a.entity_id)
  WHEN a.entity_kind='milestone' THEN (SELECT v.eng_req_id FROM delivery_req_versions v WHERE a.entity_id=printf('ms_%s_%d_%d',v.eng_req_id,v.major,v.minor) LIMIT 1)
  WHEN a.entity_kind IN ('test_case','test-case') THEN (SELECT eng_req_id FROM delivery_test_cases WHERE id=a.entity_id)
  WHEN a.entity_kind IN ('bug','buglist_item') THEN (SELECT eng_req_id FROM delivery_buglist_items WHERE id=a.entity_id)
  ELSE '' END;
`

// Each trigger mirrors one source row. Deletes are especially important:
// trash/restore and retention pruning use ordinary SQL, so the index follows
// those operations transactionally without a second store code path.
const searchTriggerSQL = `
CREATE TRIGGER IF NOT EXISTS delivery_search_req_ai AFTER INSERT ON delivery_req_versions BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'requirement_version',new.eng_req_id||':'||new.major||'.'||new.minor||'.'||new.patch,new.title,
    new.problem||char(10)||new.goals||char(10)||new.non_goals||char(10)||new.constraints_text||char(10)||new.success,
    '#/delivery/requirements/'||new.eng_req_id||'/'||new.major||'/'||new.minor||'/'||new.patch,
    e.title,COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.updated_at
  FROM delivery_eng_reqs e WHERE e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_req_au AFTER UPDATE ON delivery_req_versions BEGIN
  DELETE FROM delivery_search WHERE kind='requirement_version' AND entity_id=old.eng_req_id||':'||old.major||'.'||old.minor||'.'||old.patch;
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'requirement_version',new.eng_req_id||':'||new.major||'.'||new.minor||'.'||new.patch,new.title,
    new.problem||char(10)||new.goals||char(10)||new.non_goals||char(10)||new.constraints_text||char(10)||new.success,
    '#/delivery/requirements/'||new.eng_req_id||'/'||new.major||'/'||new.minor||'/'||new.patch,
    e.title,COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.updated_at
  FROM delivery_eng_reqs e WHERE e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_req_ad AFTER DELETE ON delivery_req_versions BEGIN
  DELETE FROM delivery_search WHERE kind='requirement_version' AND entity_id=old.eng_req_id||':'||old.major||'.'||old.minor||'.'||old.patch;
END;

CREATE TRIGGER IF NOT EXISTS delivery_search_design_ai AFTER INSERT ON delivery_designs BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'design',new.id,new.title,new.adr||char(10)||new.adr_context||char(10)||new.adr_decision||char(10)||new.adr_consequences||char(10)||new.adr_spec||char(10)||new.adr_architecture||char(10)||new.adr_sequence||char(10)||new.adr_sample,
    '#/delivery/designs/'||new.id,e.title||' · '||new.major||'.'||new.minor,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.updated_at
  FROM delivery_eng_reqs e WHERE e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_design_au AFTER UPDATE ON delivery_designs BEGIN
  DELETE FROM delivery_search WHERE kind='design' AND entity_id=old.id;
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'design',new.id,new.title,new.adr||char(10)||new.adr_context||char(10)||new.adr_decision||char(10)||new.adr_consequences||char(10)||new.adr_spec||char(10)||new.adr_architecture||char(10)||new.adr_sequence||char(10)||new.adr_sample,
    '#/delivery/designs/'||new.id,e.title||' · '||new.major||'.'||new.minor,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.updated_at
  FROM delivery_eng_reqs e WHERE e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_design_ad AFTER DELETE ON delivery_designs BEGIN
  DELETE FROM delivery_search WHERE kind='design' AND entity_id=old.id;
END;

CREATE TRIGGER IF NOT EXISTS delivery_search_design_version_ai AFTER INSERT ON delivery_design_versions BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'design',new.id,COALESCE(NULLIF(new.title,''),d.title),new.gate||char(10)||new.sub_architecture||char(10)||new.class_design,
    '#/delivery/designs/'||d.id,e.title||' · '||d.major||'.'||d.minor,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.updated_at
  FROM delivery_designs d JOIN delivery_eng_reqs e ON e.id=d.eng_req_id WHERE d.id=new.design_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_design_version_au AFTER UPDATE ON delivery_design_versions BEGIN
  DELETE FROM delivery_search WHERE kind='design' AND entity_id=old.id;
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'design',new.id,COALESCE(NULLIF(new.title,''),d.title),new.gate||char(10)||new.sub_architecture||char(10)||new.class_design,
    '#/delivery/designs/'||d.id,e.title||' · '||d.major||'.'||d.minor,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.updated_at
  FROM delivery_designs d JOIN delivery_eng_reqs e ON e.id=d.eng_req_id WHERE d.id=new.design_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_design_version_ad AFTER DELETE ON delivery_design_versions BEGIN
  DELETE FROM delivery_search WHERE kind='design' AND entity_id=old.id;
END;

CREATE TRIGGER IF NOT EXISTS delivery_search_milestone_ai AFTER INSERT ON delivery_milestones BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'milestone',new.id,new.title,new.acceptance||char(10)||new.test_note||char(10)||new.merge_note,
    '#/delivery/roadmap/m/'||COALESCE(v.major,0),COALESCE(e.title,''),
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id,''),COALESCE(new.merged_at,new.accepted_at,'')
  FROM (SELECT 1) x
  LEFT JOIN delivery_req_versions v ON new.id=printf('ms_%s_%d_%d',v.eng_req_id,v.major,v.minor) AND v.patch=1
  LEFT JOIN delivery_eng_reqs e ON e.id=v.eng_req_id LIMIT 1;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_milestone_au AFTER UPDATE ON delivery_milestones BEGIN
  DELETE FROM delivery_search WHERE kind='milestone' AND entity_id=old.id;
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'milestone',new.id,new.title,new.acceptance||char(10)||new.test_note||char(10)||new.merge_note,
    '#/delivery/roadmap/m/'||COALESCE(v.major,0),COALESCE(e.title,''),
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id,''),COALESCE(new.merged_at,new.accepted_at,'')
  FROM (SELECT 1) x
  LEFT JOIN delivery_req_versions v ON new.id=printf('ms_%s_%d_%d',v.eng_req_id,v.major,v.minor) AND v.patch=1
  LEFT JOIN delivery_eng_reqs e ON e.id=v.eng_req_id LIMIT 1;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_milestone_ad AFTER DELETE ON delivery_milestones BEGIN
  DELETE FROM delivery_search WHERE kind='milestone' AND entity_id=old.id;
END;

CREATE TRIGGER IF NOT EXISTS delivery_search_case_ai AFTER INSERT ON delivery_test_cases BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'test_case',new.id,new.title,new.body,'#/delivery/test',e.title||' · '||new.major||'.'||new.minor,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.created_at FROM delivery_eng_reqs e WHERE e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_case_au AFTER UPDATE ON delivery_test_cases BEGIN
  DELETE FROM delivery_search WHERE kind='test_case' AND entity_id=old.id;
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'test_case',new.id,new.title,new.body,'#/delivery/test',e.title||' · '||new.major||'.'||new.minor,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.created_at FROM delivery_eng_reqs e WHERE e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_case_ad AFTER DELETE ON delivery_test_cases BEGIN
  DELETE FROM delivery_search WHERE kind='test_case' AND entity_id=old.id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_bug_ai AFTER INSERT ON delivery_buglist_items BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'bug',new.id,new.title,new.body,'#/delivery/test',e.title||' · '||new.major||'.'||new.minor,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.created_at FROM delivery_eng_reqs e WHERE e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_bug_au AFTER UPDATE ON delivery_buglist_items BEGIN
  DELETE FROM delivery_search WHERE kind='bug' AND entity_id=old.id;
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'bug',new.id,new.title,new.body,'#/delivery/test',e.title||' · '||new.major||'.'||new.minor,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id),new.created_at FROM delivery_eng_reqs e WHERE e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_bug_ad AFTER DELETE ON delivery_buglist_items BEGIN
  DELETE FROM delivery_search WHERE kind='bug' AND entity_id=old.id;
END;

CREATE TRIGGER IF NOT EXISTS delivery_search_job_ai AFTER INSERT ON delivery_jobs BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'job',new.job_id,COALESCE(NULLIF(new.kind,''),'Job')||' '||new.job_id,new.error,'#/jobs',COALESCE(e.title,new.worker_id),
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id,''),new.started_at
  FROM (SELECT 1) x LEFT JOIN delivery_eng_reqs e ON e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_job_au AFTER UPDATE ON delivery_jobs BEGIN
  DELETE FROM delivery_search WHERE kind='job' AND entity_id=old.job_id;
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'job',new.job_id,COALESCE(NULLIF(new.kind,''),'Job')||' '||new.job_id,new.error,'#/jobs',COALESCE(e.title,new.worker_id),
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id,''),new.started_at
  FROM (SELECT 1) x LEFT JOIN delivery_eng_reqs e ON e.id=new.eng_req_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_job_ad AFTER DELETE ON delivery_jobs BEGIN
  DELETE FROM delivery_search WHERE kind='job' AND entity_id=old.job_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_event_ai AFTER INSERT ON delivery_job_events BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'job_event',new.job_id||':'||new.seq,COALESCE(NULLIF(new.name,''),NULLIF(new.type,''),'Job event'),
    new.text||char(10)||new.content,'#/jobs',COALESCE(e.title,j.worker_id),
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id,''),new.ts
  FROM delivery_jobs j LEFT JOIN delivery_eng_reqs e ON e.id=j.eng_req_id WHERE j.job_id=new.job_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_event_ad AFTER DELETE ON delivery_job_events BEGIN
  DELETE FROM delivery_search WHERE kind='job_event' AND entity_id=old.job_id||':'||old.seq;
END;

CREATE TRIGGER IF NOT EXISTS delivery_search_audit_ai AFTER INSERT ON delivery_audit BEGIN
  INSERT INTO delivery_search(kind,entity_id,title,body,href,scope,scope_key,updated_at)
  SELECT 'audit',CAST(new.id AS TEXT),new.summary,new.action||char(10)||new.detail,'#/delivery',
    new.entity_kind||' · '||new.entity_id,
    COALESCE(NULLIF(e.workspace_id,''),NULLIF(e.project_id,''),e.id,''),new.ts
  FROM (SELECT CASE
    WHEN new.entity_kind='requirement' THEN new.entity_id
    WHEN new.entity_kind='requirement_version' THEN substr(new.entity_id,1,instr(new.entity_id,':')-1)
    WHEN new.entity_kind='design' THEN (SELECT eng_req_id FROM delivery_designs WHERE id=new.entity_id)
    WHEN new.entity_kind='milestone' THEN (SELECT v.eng_req_id FROM delivery_req_versions v WHERE new.entity_id=printf('ms_%s_%d_%d',v.eng_req_id,v.major,v.minor) LIMIT 1)
    WHEN new.entity_kind IN ('test_case','test-case') THEN (SELECT eng_req_id FROM delivery_test_cases WHERE id=new.entity_id)
    WHEN new.entity_kind IN ('bug','buglist_item') THEN (SELECT eng_req_id FROM delivery_buglist_items WHERE id=new.entity_id)
    ELSE '' END AS eng_id) x
  LEFT JOIN delivery_eng_reqs e ON e.id=x.eng_id;
END;
CREATE TRIGGER IF NOT EXISTS delivery_search_audit_ad AFTER DELETE ON delivery_audit BEGIN
  DELETE FROM delivery_search WHERE kind='audit' AND entity_id=CAST(old.id AS TEXT);
END;

CREATE TRIGGER IF NOT EXISTS delivery_search_eng_scope_au AFTER UPDATE OF title,workspace_id,project_id ON delivery_eng_reqs BEGIN
  UPDATE delivery_search
  SET scope=CASE WHEN kind IN ('requirement_version','job','job_event') THEN new.title ELSE scope END,
      scope_key=COALESCE(NULLIF(new.workspace_id,''),NULLIF(new.project_id,''),new.id)
  WHERE (kind='requirement_version' AND substr(entity_id,1,length(old.id)+1)=old.id||':')
     OR (kind='design' AND entity_id IN (
          SELECT id FROM delivery_designs WHERE eng_req_id=old.id
          UNION SELECT v.id FROM delivery_design_versions v JOIN delivery_designs d ON d.id=v.design_id WHERE d.eng_req_id=old.id))
     OR (kind='test_case' AND entity_id IN (SELECT id FROM delivery_test_cases WHERE eng_req_id=old.id))
     OR (kind='bug' AND entity_id IN (SELECT id FROM delivery_buglist_items WHERE eng_req_id=old.id))
     OR (kind='job' AND entity_id IN (SELECT job_id FROM delivery_jobs WHERE eng_req_id=old.id))
     OR (kind='job_event' AND entity_id IN (
          SELECT ev.job_id||':'||ev.seq FROM delivery_job_events ev JOIN delivery_jobs j ON j.job_id=ev.job_id WHERE j.eng_req_id=old.id));
END;
`
