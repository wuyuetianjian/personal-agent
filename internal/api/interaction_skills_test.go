package api

import (
	"agent/internal/interaction"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const publicSkillManifest = `id: local.lookup
version: "1.0"
name: Local lookup
description: "<script>alert(1)</script>"
status: active
inputs: {}
requires: {capabilities: []}
permissions: {max_level: read_only}
privacy: {max_external_trust: local_private}
budget: {max_iterations: 3, max_tool_calls: 4}
verification: {min_coverage: 0.8, allow_conflicts: false}
workflow: {nodes: [{id: explain, role: reasoning}]}
`

func TestSkillAPIStoredOnlyLifecycle(t *testing.T) {
	for _, mode := range []string{"chat_only", "default", "plan", "acceptEdits"} {
		t.Run(mode, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			var session SessionView
			w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"s","mode":%q}`, mode))
			if w.Code != 201 {
				t.Fatal(w.Code, w.Body.String())
			}
			json.Unmarshal(w.Body.Bytes(), &session)
			s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
				t.Fatal("skill metadata resolved live provider")
				return ProviderSelection{}, nil
			}
			path := "/api/v1/sessions/" + session.ID + "/skills"
			w = apiRequest(s, "POST", path+"/validate", fmt.Sprintf(`{"manifest_text":%q}`, publicSkillManifest))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			body, _ := json.Marshal(interaction.SkillIntent{RequestID: "p", Kind: "import", ManifestText: publicSkillManifest})
			w = apiRequest(s, "POST", path+"/preview", string(body))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var p interaction.SkillOperation
			json.Unmarshal(w.Body.Bytes(), &p)
			var n int
			rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_skill_catalog`).Scan(&n)
			if n != 0 {
				t.Fatal("preview imported")
			}
			w = apiRequest(s, "POST", path+"/decision", fmt.Sprintf(`{"request_id":"d","operation_id":"p","digest":%q,"decision":"confirm"}`, p.Digest))
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, route := range []string{path, path + "/operations/p", path + "/operations/d", path + "/" + p.CatalogID, path + "/" + p.CatalogID + "/versions", path + "/" + p.CatalogID + "/versions/" + p.RevisionID} {
				w = apiRequest(s, "GET", route, "")
				if w.Code != 200 {
					t.Fatal(route, w.Code, w.Body.String())
				}
			}
			var item interaction.SkillCatalogEntry
			json.Unmarshal(apiRequest(s, "GET", path+"/"+p.CatalogID, "").Body.Bytes(), &item)
			if item.State != "disabled" || item.SelectedRevision != "" || item.Evaluation != "not_run" {
				t.Fatal(item)
			}
		})
	}
}
func TestSkillAPIRejectsNoncanonicalTransport(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	var session SessionView
	json.Unmarshal(apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"s","mode":"chat_only"}`).Body.Bytes(), &session)
	path := "/api/v1/sessions/" + session.ID + "/skills"
	body := fmt.Sprintf(`{"manifest_text":%q}`, publicSkillManifest)
	for _, bad := range []string{`{}`, `{"manifest_text":null}`, `{"manifest_text":"x","manifest_text":"y"}`, `{"manifest_text":"x","evaluation":"pass"}`, `{"manifest_text":"\ud800"}`, `{"manifest_text":"\udfff"}`, body + `{}`, fmt.Sprintf(`{"manifest_text":%q}`, strings.Repeat("x", 65537)), "{\"manifest_text\":\"\xff\"}"} {
		if w := apiRequest(s, "POST", path+"/validate", bad); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, route := range []string{path + "?owner=x", path + "?limit=0", path + "?limit=51", path + "?limit=01", path + "?limit=1&limit=2", path + "?cursor=", path + "?cursor=bad", path + "/operations/..", path + "/operations/p?after=x", path + "/", strings.Replace(path, "/skills", "/%73kills", 1), path + "%2fvalidate"} {
		if w := apiRequest(s, "GET", route, ""); w.Code != 400 {
			t.Fatal(route, w.Code, w.Body.String())
		}
	}
	// Worst-case JSON escaping must fit while the decoded artifact remains bounded.
	raw := publicSkillManifest + "#" + strings.Repeat("a", 65536-len(publicSkillManifest)-1)
	escaped := strings.ReplaceAll(fmt.Sprintf(`{"manifest_text":%q}`, raw), "a", `\u0061`)
	if w := apiRequest(s, "POST", path+"/validate", escaped); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSkillWireGolden(t *testing.T) {
	review, e := interaction.ValidateSkillManifest(publicSkillManifest)
	if e != nil {
		t.Fatal(e)
	}
	empty := review
	empty.ArtifactHash = ""
	empty.ContentHash = ""
	empty.ContentBytes = 0
	empty.CanonicalManifest = ""
	empty.Manifest = interaction.SkillManifest{}
	empty.Targets = []interaction.SkillTargetReview{}
	type hashedReview struct {
		ManifestText string                  `json:"manifest_text"`
		Review       interaction.SkillReview `json:"review"`
		ReviewHash   string                  `json:"review_sha256"`
	}
	hashed := func(text string, r interaction.SkillReview) hashedReview {
		raw, _ := json.Marshal(r)
		return hashedReview{text, r, fmt.Sprintf("%x", sha256.Sum256(raw))}
	}
	extras := []hashedReview{}
	for _, coverage := range []string{"-0.0", "0.000000001", "0.000001", "1.0"} {
		text := strings.Replace(publicSkillManifest, "min_coverage: 0.8", "min_coverage: "+coverage, 1)
		r, e := interaction.ValidateSkillManifest(text)
		if e != nil {
			t.Fatal(e)
		}
		extras = append(extras, hashed(text, r))
	}
	fixture := struct {
		ManifestText    string                  `json:"manifest_text"`
		Review          interaction.SkillReview `json:"review"`
		EmptyReview     interaction.SkillReview `json:"empty_review"`
		ReviewHash      string                  `json:"review_sha256"`
		EmptyReviewHash string                  `json:"empty_review_sha256"`
		NumericReviews  []hashedReview          `json:"numeric_reviews"`
	}{publicSkillManifest, review, empty, hashed("", review).ReviewHash, hashed("", empty).ReviewHash, extras}
	raw, _ := json.MarshalIndent(fixture, "", "  ")
	raw = append(raw, '\n')
	path := "../../protocol/v1/skills.golden.json"
	if os.Getenv("UPDATE_WIRE_FIXTURES") == "1" {
		if e := os.WriteFile(path, raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
	want, e := os.ReadFile(path)
	if e != nil || string(want) != string(raw) {
		t.Fatal("skill wire fixture drift; inspect before regenerating", e)
	}
}

func TestSkillAPIExactSelectionRecoveryAndPages(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	var session, other SessionView
	json.Unmarshal(apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"s","mode":"chat_only"}`).Body.Bytes(), &session)
	json.Unmarshal(apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"other","mode":"plan"}`).Body.Bytes(), &other)
	path := "/api/v1/sessions/" + session.ID + "/skills"
	foreign := "/api/v1/sessions/" + other.ID + "/skills"
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Fatal("live provider resolution")
		return ProviderSelection{}, nil
	}
	prepare := func(id, kind, text, catalog, revision string) interaction.SkillOperation {
		t.Helper()
		b, _ := json.Marshal(interaction.SkillIntent{RequestID: id, Kind: kind, ManifestText: text, CatalogID: catalog, RevisionID: revision})
		w := apiRequest(s, "POST", path+"/preview", string(b))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var p interaction.SkillOperation
		json.Unmarshal(w.Body.Bytes(), &p)
		return p
	}
	decide := func(p interaction.SkillOperation, id, choice string) interaction.SkillOperation {
		t.Helper()
		b, _ := json.Marshal(interaction.OperatorDecision{RequestID: id, OperationID: p.OperationID, Digest: p.Digest, Decision: choice})
		w := apiRequest(s, "POST", path+"/decision", string(b))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out interaction.SkillOperation
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	read := func(url string) []byte {
		t.Helper()
		w := apiRequest(s, "GET", url, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	first := prepare("p1", "import", publicSkillManifest, "", "")
	if out := decide(first, "d1", "confirm"); out.State != "committed" {
		t.Fatal(out)
	}
	if w := apiRequest(s, "GET", foreign+"/operations/p1", ""); w.Code != 404 {
		t.Fatal("foreign recovery", w.Code)
	}
	// Catalog visibility is service-local; only operation evidence is owner-bound.
	read(foreign + "/" + first.CatalogID)
	selected := prepare("enable", "enable", "", first.CatalogID, first.RevisionID)
	if out := decide(selected, "enabled", "confirm"); out.State != "committed" {
		t.Fatal(out)
	}
	secondText := strings.Replace(publicSkillManifest, `version: "1.0"`, `version: "2.0"`, 1)
	second := prepare("p2", "import", secondText, "", "")
	decide(second, "d2", "confirm")
	var entry interaction.SkillCatalogEntry
	json.Unmarshal(read(path+"/"+first.CatalogID), &entry)
	if entry.SelectedRevision != first.RevisionID {
		t.Fatal("import retargeted", entry)
	}
	var detail interaction.SkillRevisionRecord
	json.Unmarshal(read(path+"/"+first.CatalogID+"/versions/"+first.RevisionID), &detail)
	if detail.ManifestText != publicSkillManifest || detail.Review.Evaluation != "not_run" || detail.Review.ExecutionReason != "bridge_not_implemented" {
		t.Fatal(detail)
	}
	dup := prepare("dup", "import", publicSkillManifest, "", "")
	if out := decide(dup, "dup-confirm", "confirm"); out.State != "no_op" {
		t.Fatal(out)
	}
	bad, _ := json.Marshal(interaction.SkillIntent{RequestID: "changed", Kind: "import", ManifestText: publicSkillManifest + "# byte change\n"})
	if w := apiRequest(s, "POST", path+"/preview", string(bad)); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	cancel := prepare("cancel", "enable", "", first.CatalogID, second.RevisionID)
	if out := decide(cancel, "cancelled", "cancel"); out.State != "rejected" {
		t.Fatal(out)
	}
	before := string(read(path + "/" + first.CatalogID))
	for _, id := range []string{"p1", "d1", "enable", "enabled", "p2", "d2", "dup", "dup-confirm", "cancel", "cancelled"} {
		read(path + "/operations/" + id)
	}
	if string(read(path+"/"+first.CatalogID)) != before {
		t.Fatal("recovery changed selection")
	}
	var versions SkillVersionPage
	json.Unmarshal(read(path+"/"+first.CatalogID+"/versions?limit=1"), &versions)
	if len(versions.Items) != 1 || versions.NextCursor == "" {
		t.Fatal(versions)
	}
	var next SkillVersionPage
	json.Unmarshal(read(path+"/"+first.CatalogID+"/versions?limit=1&cursor="+versions.NextCursor), &next)
	if len(next.Items) != 1 || versions.Items[0].ID >= next.Items[0].ID {
		t.Fatal(next)
	}
	for _, url := range []string{path + "?cursor=" + versions.NextCursor, foreign + "/" + first.CatalogID + "/versions?cursor=" + versions.NextCursor, path + "/" + first.CatalogID + "/versions?cursor=" + skillCursor(session.ID, "other", first.RevisionID)} {
		if w := apiRequest(s, "GET", url, ""); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	disable := prepare("disable", "disable", "", first.CatalogID, "")
	decide(disable, "disabled", "confirm")
	noop := prepare("disabled-noop", "disable", "", first.CatalogID, "")
	if noop.Review.ArtifactHash != "" || noop.Review.Evaluation != "not_run" {
		t.Fatal(noop)
	}
	if out := decide(noop, "noop-confirm", "confirm"); out.State != "no_op" {
		t.Fatal(out)
	}
	var runs int
	if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&runs); e != nil || runs != 0 {
		t.Fatal("catalog created execution", runs, e)
	}
}
func TestSkillAPIAuthAndStrictBodies(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	var session SessionView
	json.Unmarshal(apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"s","mode":"chat_only"}`).Body.Bytes(), &session)
	path := "/api/v1/sessions/" + session.ID + "/skills"
	body, _ := json.Marshal(interaction.SkillIntent{RequestID: "p", Kind: "import", ManifestText: publicSkillManifest})
	for _, method := range []string{"GET", "POST"} {
		for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, func(r *http.Request) { r.RemoteAddr = "10.0.0.1:9" }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }} {
			r := httptest.NewRequest(method, "http://127.0.0.1:7319"+path+"/preview", strings.NewReader(string(body)))
			r.RemoteAddr = "127.0.0.1:1"
			r.Header.Set("Authorization", "Bearer secret-token")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
			change(r)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 401 && w.Code != 403 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	for _, field := range []string{"scope_id", "project_id", "evaluation", "execution", "source_path", "url", "new_value"} {
		bad := strings.Replace(string(body), `"kind":`, `"`+field+`":"caller","kind":`, 1)
		if w := apiRequest(s, "POST", path+"/preview", bad); w.Code != 400 {
			t.Fatal(field, w.Code, w.Body.String())
		}
	}
	for _, bad := range []string{strings.Replace(string(body), `"manifest_text":`, `"kind":"disable","manifest_text":`, 1), strings.Replace(string(body), `"catalog_id":""`, `"catalog_id":null`, 1), strings.Replace(string(body), `"catalog_id":"",`, "", 1), strings.Replace(string(body), `"revision_id":""`, `"revision_id":12`, 1), strings.Repeat(" ", 6*65536+4097) + string(body)} {
		if w := apiRequest(s, "POST", path+"/preview", bad); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, header := range []string{"Content-Type", "X-Agent-Protocol"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7319"+path+"/preview", strings.NewReader(string(body)))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Authorization", "Bearer secret-token")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
		r.Header.Del(header)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 415 && w.Code != 426 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var n int
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_skill_operations`).Scan(&n)
	if n != 0 {
		t.Fatal("refused request retained fake state", n)
	}
}
