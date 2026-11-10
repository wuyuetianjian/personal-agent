package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agent/internal/rag"
	"agent/internal/storage"
)

func TestProjectAssignmentRequiresVerifiedStorageRevision(t *testing.T) {
	for _, mutation := range []string{"replace", "same_caller_hash", "same_content", "chunk_update", "chunk_delete", "chunk_insert", "document_metadata"} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "rag.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = storage.Migrate(ctx, db.SQL); err != nil {
				t.Fatal(err)
			}
			store := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 100})
			original := rag.Document{ID: "d", Text: "project A verified content", ContentHash: "caller-chosen"}
			verified, err := store.IndexRevision(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			if verified.DocumentID != "d" || verified.Revision == "" || verified.Revision == original.ContentHash || len(verified.Chunks) == 0 {
				t.Fatal(verified)
			}
			switch mutation {
			case "replace", "same_caller_hash", "same_content":
				replacement := rag.Document{ID: "d", Text: "project B PRIVATE replacement"}
				if mutation == "same_caller_hash" {
					replacement.ContentHash = original.ContentHash
				}
				if mutation == "same_content" {
					replacement = original
				}
				changed, err := store.IndexRevision(ctx, replacement)
				if err != nil {
					t.Fatal(err)
				}
				if changed.Revision == verified.Revision {
					t.Fatal("index reused storage revision")
				}
			case "chunk_update":
				_, err = db.SQL.ExecContext(ctx, "UPDATE document_chunks SET text='project B PRIVATE chunk' WHERE document_id='d'")
			case "chunk_delete":
				_, err = db.SQL.ExecContext(ctx, "DELETE FROM document_chunks WHERE document_id='d'")
			case "chunk_insert":
				_, err = db.SQL.ExecContext(ctx, `INSERT INTO document_chunks(id,document_id,chunk_index,text,token_count,content_hash,metadata_json,created_at) SELECT 'extra',document_id,99,'project B PRIVATE extra',4,'caller-chosen','{}',created_at FROM document_chunks WHERE document_id='d' LIMIT 1`)
			case "document_metadata":
				_, err = db.SQL.ExecContext(ctx, "UPDATE documents SET title='project B title' WHERE id='d'")
			}
			if err != nil {
				t.Fatal(err)
			}
			// Deterministic A-index -> B-replace -> A-assign interleaving. Assignment
			// cannot adopt whatever happens to be current under the shared document ID.
			if err = AssignDocumentProject(ctx, db.SQL, "d", "projectA", verified.Revision); !errors.Is(err, rag.ErrDocumentRevisionChanged) {
				t.Fatal("stale assignment accepted", err)
			}
			rows, err := store.ListProjectChunks(ctx, "projectA")
			if err != nil || len(rows) != 0 {
				t.Fatal("cross-project corpus leak", rows, err)
			}
			current, err := store.IndexRevision(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			if err = AssignDocumentProject(ctx, db.SQL, "d", "projectA", current.Revision); err != nil {
				t.Fatal(err)
			}
			if err = AssignDocumentProject(ctx, db.SQL, "d", "projectA", current.Revision); err != nil {
				t.Fatal("same-revision assignment not idempotent", err)
			}
			if err = AssignDocumentProject(ctx, db.SQL, "d", "projectB", current.Revision); err == nil {
				t.Fatal("changed current owner")
			}
			rows, err = store.ListProjectChunks(ctx, "projectA")
			if err != nil || len(rows) != 1 || rows[0].Chunk.Text != original.Text {
				t.Fatal(rows, err)
			}
			if _, err = db.SQL.ExecContext(ctx, "UPDATE document_chunks SET text='late foreign chunk' WHERE document_id='d'"); err != nil {
				t.Fatal(err)
			}
			rows, err = store.ListProjectChunks(ctx, "projectA")
			if err != nil || len(rows) != 0 {
				t.Fatal("post-grant mutation retained ownership", rows, err)
			}
			if err = AssignDocumentProject(ctx, db.SQL, "d", "projectA", current.Revision); !errors.Is(err, rag.ErrDocumentRevisionChanged) {
				t.Fatal("post-grant stale revision reauthorized", err)
			}
			if err = AssignDocumentProject(ctx, db.SQL, "d", "projectA", ""); !errors.Is(err, rag.ErrDocumentRevisionChanged) {
				t.Fatal("empty generation accepted", err)
			}
		})
	}
}
