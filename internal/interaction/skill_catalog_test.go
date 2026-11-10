package interaction

import (
	"agent/internal/storage"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func skillPrepare(t *testing.T, s *Store, id, raw string) SkillOperation {
	t.Helper()
	p, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: id, Kind: "import", ManifestText: raw})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func skillDecide(t *testing.T, s *Store, p SkillOperation, id, decision string) SkillOperation {
	t.Helper()
	r, e := s.DecideSkill(t.Context(), "s", OperatorDecision{RequestID: id, OperationID: p.OperationID, Digest: p.Digest, Decision: decision})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestSkillCatalogImmutableLifecycle(t *testing.T) {
	s, _ := queueSetup(t)
	c := t.Context()
	p := skillPrepare(t, s, "p", catalogManifest)
	for _, table := range []string{"interaction_skill_catalog", "interaction_skill_revisions", "skills", "interaction_runs", "interaction_jobs"} {
		var n int
		if e := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	if p.State != "pending" || p.Review.Manifest.Status != "active" || p.NewState != "disabled" || p.Review.ExecutionReason != "bridge_not_implemented" {
		t.Fatal(p)
	}
	out := skillDecide(t, s, p, "d", "confirm")
	if out.State != "committed" {
		t.Fatal(out)
	}
	item, e := s.SkillCatalogItem(c, "s", out.CatalogID)
	if e != nil || item.State != "disabled" || item.SelectedRevision != "" || item.Revision != 1 || item.Evaluation != "not_run" || item.Execution != "unavailable" {
		t.Fatal(item, e)
	}
	rev, e := s.SkillRevision(c, "s", out.CatalogID, out.RevisionID)
	if e != nil || rev.ManifestText != catalogManifest || rev.Review.ArtifactHash != p.Review.ArtifactHash || rev.Provenance.OperationID != "p" {
		t.Fatal(rev, e)
	}
	duplicate := skillPrepare(t, s, "duplicate", catalogManifest)
	if got := skillDecide(t, s, duplicate, "duplicate-d", "confirm"); got.State != "no_op" || got.RevisionID != out.RevisionID {
		t.Fatal(got)
	}
	if _, e = s.PrepareSkill(c, "s", SkillIntent{RequestID: "different-bytes", Kind: "import", ManifestText: catalogManifest + "# changed\n"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	enable, e := s.PrepareSkill(c, "s", SkillIntent{RequestID: "enable", Kind: "enable", CatalogID: out.CatalogID, RevisionID: out.RevisionID})
	if e != nil {
		t.Fatal(e)
	}
	if got := skillDecide(t, s, enable, "enable-d", "confirm"); got.State != "committed" {
		t.Fatal(got)
	}
	v2 := skillDecide(t, s, skillPrepare(t, s, "v2", strings.Replace(catalogManifest, "1.0.0", "2.0.0", 1)), "v2-d", "confirm")
	item, e = s.SkillCatalogItem(c, "s", out.CatalogID)
	if e != nil || item.SelectedRevision != out.RevisionID || item.State != "enabled" || item.Revision != 3 {
		t.Fatal(item, e)
	}
	enable, e = s.PrepareSkill(c, "s", SkillIntent{RequestID: "enable-v2", Kind: "enable", CatalogID: out.CatalogID, RevisionID: v2.RevisionID})
	if e != nil {
		t.Fatal(e)
	}
	skillDecide(t, s, enable, "enable-v2-d", "confirm")
	disable, e := s.PrepareSkill(c, "s", SkillIntent{RequestID: "disable", Kind: "disable", CatalogID: out.CatalogID})
	if e != nil {
		t.Fatal(e)
	}
	skillDecide(t, s, disable, "disable-d", "confirm")
	after, e := s.SkillRevision(c, "s", out.CatalogID, out.RevisionID)
	if e != nil || !reflect.DeepEqual(after, rev) {
		t.Fatal("revision mutated", e)
	}
	versions, e := s.SkillVersions(c, "s", out.CatalogID, "", 50)
	if e != nil || len(versions) != 2 {
		t.Fatal(versions, e)
	}
	for _, q := range []string{`UPDATE interaction_skill_revisions SET raw_artifact='changed'`, `DELETE FROM interaction_skill_revisions`, `UPDATE interaction_skill_catalog SET generation='new'`, `DELETE FROM interaction_skill_catalog`, `UPDATE interaction_skill_history SET state='enabled'`, `DELETE FROM interaction_skill_history`, `UPDATE interaction_skill_operations SET staged='changed'`, `DELETE FROM interaction_skill_operations`, `UPDATE interaction_skill_requests SET result='changed'`, `DELETE FROM interaction_skill_requests`} {
		if _, e = s.db.Exec(q); e == nil {
			t.Fatal("mutable evidence", q)
		}
	}
	for _, table := range []string{"skills", "interaction_runs", "interaction_jobs", "documents", "project_document_scopes"} {
		var n int
		if e := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}
func TestSkillCatalogReplayConcurrentAndForeign(t *testing.T) {
	s, _ := queueSetup(t)
	p := skillPrepare(t, s, "p", catalogManifest)
	again := skillPrepare(t, s, "p", catalogManifest)
	if !reflect.DeepEqual(p, again) {
		t.Fatal("preview replay")
	}
	var wg sync.WaitGroup
	results := make(chan SkillOperation, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.DecideSkill(t.Context(), "s", OperatorDecision{RequestID: "d", OperationID: "p", Digest: p.Digest, Decision: "confirm"})
			if e != nil {
				t.Error(e)
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	for r := range results {
		if r.State != "committed" {
			t.Fatal(r)
		}
	}
	for _, req := range []string{"p", "d"} {
		r, e := s.SkillOperation(t.Context(), "s", req)
		if e != nil || r.State != "committed" {
			t.Fatal(r, e)
		}
		if _, e = s.SkillOperation(t.Context(), "other", req); e == nil {
			t.Fatal("foreign recovery")
		}
	}
	if _, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "p", Kind: "import", ManifestText: catalogManifest + "# changed"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.DecideSkill(t.Context(), "s", OperatorDecision{RequestID: "second", OperationID: "p", Digest: p.Digest, Decision: "confirm"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM security_audit_log WHERE action='skill.import'`).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
}
func TestSkillCatalogStalePolicyReviewExpiryCancel(t *testing.T) {
	for _, kind := range []string{"generation", "revision", "selection", "policy", "review", "expiry", "cancel", "foreign", "digest"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := queueSetup(t)
			base := skillDecide(t, s, skillPrepare(t, s, "base", catalogManifest), "base-d", "confirm")
			p, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "p", Kind: "enable", CatalogID: base.CatalogID, RevisionID: base.RevisionID})
			if e != nil {
				t.Fatal(e)
			}
			now := time.Now()
			decision, session := "confirm", "s"
			switch kind {
			case "generation":
				_, e = s.db.Exec(`DROP TRIGGER interaction_skill_catalog_identity; UPDATE interaction_skill_catalog SET generation='changed',revision=revision+1`)
			case "revision":
				_, e = s.db.Exec(`UPDATE interaction_skill_catalog SET revision=revision+1`)
			case "selection":
				q, err := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "other", Kind: "enable", CatalogID: base.CatalogID, RevisionID: base.RevisionID})
				if err != nil {
					t.Fatal(err)
				}
				skillDecide(t, s, q, "other-d", "confirm")
			case "policy":
				_, e = s.db.Exec(`DROP TRIGGER interaction_api_binding_immutable; UPDATE interaction_api_sessions SET binding='changed'`)
			case "review":
				var raw string
				if e = s.db.QueryRow(`SELECT preview FROM interaction_skill_operations WHERE id='p'`).Scan(&raw); e != nil {
					t.Fatal(e)
				}
				_, e = s.db.Exec(`DROP TRIGGER interaction_skill_intent_immutable; DROP TRIGGER interaction_skill_terminal; UPDATE interaction_skill_operations SET preview=? WHERE id='p'`, strings.ReplaceAll(raw, SkillReviewVersion, "old-review"))
			case "expiry":
				now = now.Add(11 * time.Minute)
			case "cancel":
				decision = "cancel"
			case "foreign":
				session = "other"
				if e = s.CreateSession(t.Context(), session); e != nil {
					t.Fatal(e)
				}
			case "digest":
				p.Digest = strings.Repeat("0", 64)
			}
			if e != nil {
				t.Fatal(e)
			}
			out, e := s.decideSkill(t.Context(), session, OperatorDecision{RequestID: "d", OperationID: p.OperationID, Digest: p.Digest, Decision: decision}, now)
			if kind == "review" || kind == "foreign" || kind == "digest" {
				if !errors.Is(e, ErrConflict) {
					t.Fatal(e)
				}
				return
			}
			if e != nil || (out.State != "rejected" && out.State != "expired") {
				t.Fatal(out, e)
			}
			if kind != "selection" {
				item, e := s.SkillCatalogItem(t.Context(), "s", base.CatalogID)
				if e != nil || item.State != "disabled" {
					t.Fatal(item, e)
				}
			}
		})
	}
}
func TestSkillCatalogRollbackEachWrite(t *testing.T) {
	for _, stage := range []string{"prepare_operations", "prepare_requests", "prepare_audit", "catalog", "revisions", "history", "result", "requests", "audit"} {
		t.Run(stage, func(t *testing.T) {
			s, _ := queueSetup(t)
			var p SkillOperation
			if !strings.HasPrefix(stage, "prepare_") {
				p = skillPrepare(t, s, "p", catalogManifest)
			}
			tables := map[string]string{"prepare_operations": "interaction_skill_operations", "prepare_requests": "interaction_skill_requests", "prepare_audit": "security_audit_log", "catalog": "interaction_skill_catalog", "revisions": "interaction_skill_revisions", "history": "interaction_skill_history", "result": "interaction_skill_operations", "requests": "interaction_skill_requests", "audit": "security_audit_log"}
			event := "INSERT"
			if stage == "result" {
				event = "UPDATE"
			}
			if _, e := s.db.Exec(fmt.Sprintf(`CREATE TRIGGER inject_failure BEFORE %s ON %s BEGIN SELECT RAISE(ABORT,'injected'); END`, event, tables[stage])); e != nil {
				t.Fatal(e)
			}
			var e error
			if strings.HasPrefix(stage, "prepare_") {
				_, e = s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "p", Kind: "import", ManifestText: catalogManifest})
			} else {
				_, e = s.DecideSkill(t.Context(), "s", OperatorDecision{RequestID: "d", OperationID: "p", Digest: p.Digest, Decision: "confirm"})
			}
			if e == nil {
				t.Fatal("failure ignored")
			}
			for _, table := range []string{"interaction_skill_catalog", "interaction_skill_revisions", "interaction_skill_history"} {
				var n int
				if e = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
					t.Fatal(table, n, e)
				}
			}
			var n int
			want := 1
			if strings.HasPrefix(stage, "prepare_") {
				want = 0
			}
			if e = s.db.QueryRow(`SELECT count(*) FROM interaction_skill_requests`).Scan(&n); e != nil || n != want {
				t.Fatal(n, e)
			}
			if want == 1 {
				r, e := s.SkillOperation(t.Context(), "s", "p")
				if e != nil || r.State != "pending" {
					t.Fatal(r, e)
				}
			}
		})
	}
}
func TestSkillCatalogDurableReopenAndBoundedReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	c := t.Context()
	open := func() *sql.DB {
		db, e := storage.OpenSQLite(c, path)
		if e != nil {
			t.Fatal(e)
		}
		if e = storage.Migrate(c, db.SQL); e != nil {
			t.Fatal(e)
		}
		return db.SQL
	}
	db := open()
	s := NewStore(db)
	if e := s.CreateSession(c, "s"); e != nil {
		t.Fatal(e)
	}
	if e := s.BindChatOnly(c, "s", ChatOnlyPolicy{}); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO interaction_api_sessions VALUES('s','create','snapshot','chat_only')`); e != nil {
		t.Fatal(e)
	}
	p := skillPrepare(t, s, "p", catalogManifest)
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
	db = open()
	s = NewStore(db)
	pending, e := s.SkillOperation(c, "s", "p")
	if e != nil || !reflect.DeepEqual(p, pending) {
		t.Fatal(e)
	}
	out := skillDecide(t, s, p, "d", "confirm")
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db = open()
	defer db.Close()
	s = NewStore(db)
	for _, id := range []string{"p", "d"} {
		r, e := s.SkillOperation(c, "s", id)
		if e != nil || !reflect.DeepEqual(out, r) {
			t.Fatal(r, e)
		}
	}
	list, e := s.ListSkills(c, "s", "", 1)
	if e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	tail, e := s.ListSkills(c, "s", list[0].ID, 1)
	if e != nil || len(tail) != 0 {
		t.Fatal(tail, e)
	}
	for _, limit := range []int{-1, 0, 51} {
		if _, e = s.ListSkills(c, "s", "", limit); e == nil {
			t.Fatal("list bound")
		}
		if _, e = s.SkillVersions(c, "s", out.CatalogID, "", limit); e == nil {
			t.Fatal("versions bound")
		}
	}
	if _, e = s.ListSkills(c, "unknown", "", 10); e == nil {
		t.Fatal("unbound reader")
	}
	if _, e = s.SkillRevision(c, "s", out.CatalogID, "latest"); e == nil {
		t.Fatal("latest resolved")
	}
}
func TestSkillCatalogMetadataModes(t *testing.T) {
	for _, mode := range []PermissionMode{ModeDefault, ModeAcceptEdits, ModePlan} {
		t.Run(string(mode), func(t *testing.T) {
			s, _ := knowledgeSetup(t, mode)
			p := skillPrepare(t, s, "p", catalogManifest)
			out := skillDecide(t, s, p, "d", "confirm")
			if out.State != "committed" {
				t.Fatal(out)
			}
		})
	}
	// chat_only lifecycle is covered above. No workspace filesystem needed.
}

func TestSkillCatalogStorageRejectsUnboundedRows(t *testing.T) {
	s, _ := queueSetup(t)
	if _, e := s.db.Exec(`INSERT INTO interaction_skill_catalog VALUES(?,?,?,1,'disabled','')`, strings.Repeat("x", 129), "bounded", "generation"); e == nil {
		t.Fatal("unbounded catalog ID accepted")
	}
}
func TestSkillCatalogReviewUpgradeRequiresFreshRevision(t *testing.T) {
	s, _ := queueSetup(t)
	out := skillDecide(t, s, skillPrepare(t, s, "p", catalogManifest), "d", "confirm")
	var raw string
	if e := s.db.QueryRow(`SELECT review FROM interaction_skill_revisions WHERE id=?`, out.RevisionID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	// Simulate a retained artifact reviewed by an older kernel version, not a
	// permitted mutation through the production API.
	if _, e := s.db.Exec(`DROP TRIGGER interaction_skill_revision_immutable; UPDATE interaction_skill_revisions SET review_version='older-profile',review=? WHERE id=?`, strings.ReplaceAll(raw, SkillReviewVersion, "older-profile"), out.RevisionID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "duplicate", Kind: "import", ManifestText: catalogManifest}); !errors.Is(e, ErrConflict) {
		t.Fatal("old review silently replaced by duplicate preview", e)
	}
}

func TestSkillCatalogDistinctDecisionRaceAndNoopSelection(t *testing.T) {
	s, _ := queueSetup(t)
	p := skillPrepare(t, s, "p", catalogManifest)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, e := s.DecideSkill(t.Context(), "s", OperatorDecision{RequestID: fmt.Sprintf("d%d", i), OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
			mu.Lock()
			defer mu.Unlock()
			if e == nil && out.State == "committed" {
				wins++
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e, out.State)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatal(wins)
	}
	for _, kind := range []string{"disable", "enable", "enable", "disable", "disable"} {
		in := SkillIntent{RequestID: fmt.Sprintf("select-%s-%d", kind, wins), Kind: kind, CatalogID: p.CatalogID}
		if kind == "enable" {
			in.RevisionID = p.RevisionID
		}
		q, e := s.PrepareSkill(t.Context(), "s", in)
		if e != nil {
			t.Fatal(e)
		}
		out := skillDecide(t, s, q, in.RequestID+"-d", "confirm")
		want := "no_op"
		if wins == 2 || wins == 4 {
			want = "committed"
		}
		if out.State != want {
			t.Fatal(kind, wins, out)
		}
		wins++
	}
}
func TestSkillCatalogRechecksArtifactAndVersions(t *testing.T) {
	for _, which := range []string{"staging", "review_version", "parser_version", "review_hash", "review_content"} {
		t.Run(which, func(t *testing.T) {
			s, _ := queueSetup(t)
			p := skillPrepare(t, s, "p", catalogManifest)
			if _, e := s.db.Exec(`DROP TRIGGER interaction_skill_intent_immutable; DROP TRIGGER interaction_skill_terminal`); e != nil {
				t.Fatal(e)
			}
			switch which {
			case "staging":
				if _, e := s.db.Exec(`UPDATE interaction_skill_operations SET staged=? WHERE id='p'`, strings.Replace(catalogManifest, "active", "draft", 1)); e != nil {
					t.Fatal(e)
				}
			case "review_version":
				p.ReviewVersion = "old"
			case "parser_version":
				p.ParserVersion = "old"
			case "review_hash":
				p.ReviewHash = strings.Repeat("0", 64)
			case "review_content":
				p.Review.Manifest.Budget.MaxToolCalls++
				p.ReviewHash = operatorHash(p.Review)
			}
			if which != "staging" {
				p.Digest = skillDigest(p)
				raw, _ := json.Marshal(p)
				if _, e := s.db.Exec(`UPDATE interaction_skill_operations SET preview=? WHERE id='p'`, string(raw)); e != nil {
					t.Fatal(e)
				}
			}
			out := skillDecide(t, s, p, "d", "confirm")
			if out.State != "rejected" {
				t.Fatal(out)
			}
			var n int
			if e := s.db.QueryRow(`SELECT count(*) FROM interaction_skill_catalog`).Scan(&n); e != nil || n != 0 {
				t.Fatal(n, e)
			}
		})
	}
}
func TestSkillCatalogLimitsAndInvalidIntent(t *testing.T) {
	s, _ := queueSetup(t)
	for _, in := range []SkillIntent{
		{RequestID: "bad", Kind: "run", ManifestText: catalogManifest},
		{RequestID: "bad", Kind: "import", CatalogID: "selected", ManifestText: catalogManifest},
		{RequestID: "bad", Kind: "import", RevisionID: "selected", ManifestText: catalogManifest},
		{RequestID: "bad", Kind: "enable", CatalogID: "selected"},
		{RequestID: "bad", Kind: "enable", CatalogID: "selected", RevisionID: "rev", ManifestText: catalogManifest},
		{RequestID: "bad", Kind: "disable", CatalogID: "selected", RevisionID: "rev"},
		{RequestID: "../bad", Kind: "import", ManifestText: catalogManifest},
		{RequestID: "bad", Kind: "import", ManifestText: strings.Repeat("x", MaxSkillManifestBytes+1)},
	} {
		if _, e := s.PrepareSkill(t.Context(), "s", in); e == nil {
			t.Fatal("accepted", in.Kind)
		}
	}
	for i := 0; i < 64; i++ {
		skillPrepare(t, s, fmt.Sprintf("pending-%d", i), catalogManifest)
	}
	if _, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "over", Kind: "import", ManifestText: catalogManifest}); !errors.Is(e, ErrBudget) {
		t.Fatal("pending capacity", e)
	}
	// Both catalog and operation retention are bounded without purging originals.
	for _, kind := range []string{"catalog", "operations", "reserved", "revision_count", "revision_bytes"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := queueSetup(t)
			var e error
			switch kind {
			case "catalog":
				_, e = s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<256) INSERT INTO interaction_skill_catalog SELECT 'seed-'||x,'seed-'||x,'generation-'||x,1,'disabled','' FROM n`)
			case "operations":
				_, e = s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<4096) INSERT INTO interaction_skill_operations SELECT 'seed-'||x,'s','{}','',16392,'expired','{}' FROM n`)
			case "reserved":
				_, e = s.db.Exec(`INSERT INTO interaction_skill_operations VALUES('seed','s','{}','',67108864,'expired','{}')`)
			case "revision_count", "revision_bytes":
				base := skillDecide(t, s, skillPrepare(t, s, "base", catalogManifest), "base-d", "confirm")
				count, rawSize := 1024, 1
				if kind == "revision_bytes" {
					count, rawSize = 513, 65536
				}
				_, e = s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO interaction_skill_revisions SELECT 'seed-'||x,?,'seed-'||x,?,?,'{}',?,'parser','review',?,'{}' FROM n`, count, base.CatalogID, strings.Repeat("x", rawSize), strings.Repeat("0", 64), strings.Repeat("0", 64), strings.Repeat("x", rawSize))
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "over", Kind: "import", ManifestText: strings.Replace(catalogManifest, "local.lookup", "another.lookup", 1)}); !errors.Is(e, ErrBudget) {
				t.Fatal(kind, e)
			}
		})
	}
}
func TestSkillCatalogDecisionCapacityRecheck(t *testing.T) {
	s, _ := queueSetup(t)
	p := skillPrepare(t, s, "p", catalogManifest)
	if _, e := s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<256) INSERT INTO interaction_skill_catalog SELECT 'seed-'||x,'seed-'||x,'generation-'||x,1,'disabled','' FROM n`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DecideSkill(t.Context(), "s", OperatorDecision{RequestID: "d", OperationID: "p", Digest: p.Digest, Decision: "confirm"}); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	out, e := s.SkillOperation(t.Context(), "s", "p")
	if e != nil || out.State != "pending" {
		t.Fatal(out, e)
	}
}
func TestSkillCatalogEnableRollbackAndSameSessionScopeChange(t *testing.T) {
	for _, kind := range []string{"catalog_update", "history", "scope"} {
		t.Run(kind, func(t *testing.T) {
			s, w := knowledgeSetup(t, ModePlan)
			base := skillDecide(t, s, skillPrepare(t, s, "base", catalogManifest), "base-d", "confirm")
			p, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "p", Kind: "enable", CatalogID: base.CatalogID, RevisionID: base.RevisionID})
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "catalog_update":
				_, e = s.db.Exec(`CREATE TRIGGER inject_failure BEFORE UPDATE ON interaction_skill_catalog BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "history":
				_, e = s.db.Exec(`CREATE TRIGGER inject_failure BEFORE INSERT ON interaction_skill_history BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "scope":
				_, e = s.db.Exec(`DROP TRIGGER interaction_workspace_immutable; UPDATE interaction_workspaces SET scope_id=? WHERE session_id='s'`, w.ScopeID+"changed")
			}
			if e != nil {
				t.Fatal(e)
			}
			out, e := s.DecideSkill(t.Context(), "s", OperatorDecision{RequestID: "d", OperationID: "p", Digest: p.Digest, Decision: "confirm"})
			if kind == "scope" {
				if e != nil || out.State != "rejected" {
					t.Fatal(out, e)
				}
			} else if e == nil {
				t.Fatal("failure ignored")
			}
			item, e := s.SkillCatalogItem(t.Context(), "s", base.CatalogID)
			if e != nil || item.State != "disabled" || item.Revision != 1 {
				t.Fatal(item, e)
			}
		})
	}
}

func TestSkillCatalogDisabledNoopReviewStillReportsNoExecution(t *testing.T) {
	s, _ := queueSetup(t)
	base := skillDecide(t, s, skillPrepare(t, s, "base", catalogManifest), "base-d", "confirm")
	p, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "p", Kind: "disable", CatalogID: base.CatalogID})
	if e != nil {
		t.Fatal(e)
	}
	if p.Review.Execution != "unavailable" || p.Review.Evaluation != "not_run" || p.Review.ExecutionReason != "bridge_not_implemented" || p.RevisionID != "" {
		t.Fatal(p.Review)
	}
	if r := skillDecide(t, s, p, "d", "confirm"); r.State != "no_op" {
		t.Fatal(r)
	}
}

func TestSkillCatalogNeverAdoptsLegacyActiveVersion(t *testing.T) {
	s, _ := queueSetup(t)
	if _, e := s.db.Exec(`INSERT INTO skills VALUES('local.lookup','legacy-name','legacy-desc','1.0.0','active','legacy-source',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP); INSERT INTO skill_versions VALUES('local.lookup','1.0.0','legacy-raw','legacy-hash',CURRENT_TIMESTAMP)`); e != nil {
		t.Fatal(e)
	}
	p := skillPrepare(t, s, "p", catalogManifest)
	out := skillDecide(t, s, p, "d", "confirm")
	q, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "enable", Kind: "enable", CatalogID: out.CatalogID, RevisionID: out.RevisionID})
	if e != nil {
		t.Fatal(e)
	}
	skillDecide(t, s, q, "enable-d", "confirm")
	var name, status, raw, hash string
	if e = s.db.QueryRow(`SELECT s.name,s.status,v.manifest_json,v.checksum FROM skills s JOIN skill_versions v ON v.skill_id=s.id WHERE s.id='local.lookup'`).Scan(&name, &status, &raw, &hash); e != nil || name != "legacy-name" || status != "active" || raw != "legacy-raw" || hash != "legacy-hash" {
		t.Fatal(name, status, raw, hash, e)
	}
}
func TestSkillCatalogImmutableEvidenceRejectsReplace(t *testing.T) {
	s, _ := queueSetup(t)
	skillDecide(t, s, skillPrepare(t, s, "p", catalogManifest), "d", "confirm")
	for _, table := range []string{"interaction_skill_catalog", "interaction_skill_revisions", "interaction_skill_operations", "interaction_skill_requests", "interaction_skill_history"} {
		if _, e := s.db.Exec(`INSERT OR REPLACE INTO ` + table + ` SELECT * FROM ` + table); e == nil {
			t.Fatal("replaced immutable evidence", table)
		}
	}
}
