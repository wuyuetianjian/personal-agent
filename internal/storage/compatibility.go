package storage

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// ErrIncompatibleSchema intentionally contains no source-controlled names, SQL,
// paths or record values. Recognition is not an import/data-integrity attestation.
var ErrIncompatibleSchema = errors.New("unsupported database schema or migration history; preserve the source and use a fresh runtime parent")

const migrationLedgerDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (version text primary key, applied_at timestamp not null DEFAULT CURRENT_TIMESTAMP)`

// Provider evidence predates ledger ownership of this optional runtime table.
// Keep accepting its exact known shape; do not treat arbitrary extra tables as safe.
const ProviderEvidenceDDL = `CREATE TABLE IF NOT EXISTS interaction_provider_evidence(model_id TEXT PRIMARY KEY, evidence TEXT NOT NULL)`

type schemaObject struct{ Type, Name, Table, SQL string }
type schemaQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func readCatalog(ctx context.Context, db schemaQuery) ([]schemaObject, error) {
	rows, err := db.QueryContext(ctx, `SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_schema WHERE substr(name,1,7)<>'sqlite_' ORDER BY type,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var objects []schemaObject
	for rows.Next() {
		var obj schemaObject
		if err = rows.Scan(&obj.Type, &obj.Name, &obj.Table, &obj.SQL); err != nil {
			return nil, err
		}
		objects = append(objects, obj)
	}
	return objects, rows.Err()
}

var referenceSchemas struct {
	sync.Once
	catalogs [][]schemaObject
	provider schemaObject
	err      error
}

// Build references only from embedded SQL in an isolated in-memory database.
// Exact SQLite catalog SQL comparison is deliberately conservative: logically
// equivalent but differently authored schema is not a recognized lineage.
func loadReferenceSchemas() {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		referenceSchemas.err = err
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	names, err := migrationNames()
	if err != nil {
		referenceSchemas.err = err
		return
	}
	referenceSchemas.catalogs = make([][]schemaObject, len(names)+1)
	for i, name := range names {
		raw, e := migrationFiles.ReadFile("migrations/" + name)
		if e != nil {
			referenceSchemas.err = e
			return
		}
		if _, e = db.ExecContext(ctx, string(raw)); e != nil {
			referenceSchemas.err = e
			return
		}
		referenceSchemas.catalogs[i+1], e = readCatalog(ctx, db)
		if e != nil {
			referenceSchemas.err = e
			return
		}
	}
	if _, err = db.ExecContext(ctx, ProviderEvidenceDDL); err != nil {
		referenceSchemas.err = err
		return
	}
	objects, err := readCatalog(ctx, db)
	if err != nil {
		referenceSchemas.err = err
		return
	}
	for _, obj := range objects {
		if obj.Name == "interaction_provider_evidence" {
			referenceSchemas.provider = obj
			return
		}
	}
	referenceSchemas.err = errors.New("missing reference provider schema")
}

func recognizedLedger(obj schemaObject) bool {
	// Both historical repository fixtures and the production ledger are supported.
	// Only the fixture's applied_at nullability differs. No arbitrary extra SQL.
	folded := strings.ToLower(strings.Join(strings.Fields(obj.SQL), " "))
	folded = strings.NewReplacer(" (", "(", "( ", "(", " )", ")", ", ", ",", " ,", ",").Replace(folded)
	return obj.Type == "table" && obj.Table == "schema_migrations" &&
		(folded == "create table schema_migrations(version text primary key,applied_at timestamp not null default current_timestamp)" ||
			folded == "create table schema_migrations(version text primary key,applied_at timestamp default current_timestamp)")
}

// ValidateSchema reads one snapshot. It accepts empty databases or exact known
// contiguous migration prefixes with matching catalog definitions (plus the
// optional known runtime evidence table). It does not validate business records,
// authenticate origin, attest historic SQL execution, or certify an import.
// Callers must exclude other writers for the entire validation/upgrade lifecycle.
func ValidateSchema(ctx context.Context, db *sql.DB) error {
	referenceSchemas.Do(loadReferenceSchemas)
	if referenceSchemas.err != nil {
		return errors.New("cannot construct embedded schema reference")
	}
	names, err := migrationNames()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ErrIncompatibleSchema
	}
	defer tx.Rollback()
	objects, err := readCatalog(ctx, tx)
	if err != nil {
		return ErrIncompatibleSchema
	}
	var catalog []schemaObject
	var ledger, provider bool
	for _, obj := range objects {
		switch obj.Name {
		case "schema_migrations":
			if !recognizedLedger(obj) {
				return ErrIncompatibleSchema
			}
			ledger = true
		case "interaction_provider_evidence":
			if obj != referenceSchemas.provider {
				return ErrIncompatibleSchema
			}
			provider = true
		default:
			catalog = append(catalog, obj)
		}
	}
	if !ledger {
		if len(objects) != 0 {
			return ErrIncompatibleSchema
		}
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT version, typeof(version) FROM schema_migrations ORDER BY version`)
	if err != nil {
		return ErrIncompatibleSchema
	}
	prefix := 0
	for rows.Next() {
		// Scan can convert BLOB bytes to a Go string, but migrationApplied's
		// TEXT parameter does not match a stored BLOB. Require the storage class
		// as well as the filename, without casting or repairing source identities.
		var version, storageClass string
		if err = rows.Scan(&version, &storageClass); err != nil || storageClass != "text" || prefix >= len(names) || version != names[prefix] {
			rows.Close()
			return ErrIncompatibleSchema
		}
		prefix++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrIncompatibleSchema
	}
	var invalid int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE typeof(applied_at)<>'text' OR datetime(applied_at) IS NULL`).Scan(&invalid); err != nil || invalid != 0 {
		return ErrIncompatibleSchema
	}
	// Runtime evidence was first used with the service schema (0016).
	if provider && prefix < 16 {
		return ErrIncompatibleSchema
	}
	if !reflect.DeepEqual(catalog, referenceSchemas.catalogs[prefix]) {
		return ErrIncompatibleSchema
	}
	return nil
}

// ValidateSQLiteFile performs a WAL-aware, mode=ro preflight without changing
// journal mode or checkpointing. Do not use immutable=1: it ignores committed WAL.
// SQLite may maintain/rebuild shared-memory WAL bookkeeping; logical database and
// committed WAL are read-only. The owner must hold its process lock, with no
// noncooperating writer, until the subsequent writable open/upgrade is complete.
func ValidateSQLiteFile(ctx context.Context, path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ErrIncompatibleSchema
	}
	uriPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return ErrIncompatibleSchema
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	return ValidateSchema(ctx, db)
}
