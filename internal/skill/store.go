package skill

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("skill not found")

type Store struct {
	DB *sql.DB
}

type Record struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	ActiveVersion string    `json:"active_version"`
	Status        Status    `json:"status"`
	Source        string    `json:"source"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type VersionRecord struct {
	Manifest  Manifest  `json:"manifest"`
	Checksum  string    `json:"checksum"`
	CreatedAt time.Time `json:"created_at"`
}

func (s Store) Import(ctx context.Context, manifest Manifest, source string) error {
	manifest = Normalize(manifest)
	if manifest.Status == "" {
		manifest.Status = StatusDraft
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	sum, err := Checksum(manifest)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_versions (skill_id,version,manifest_json,checksum,created_at) VALUES (?,?,?,?,?) ON CONFLICT(skill_id,version) DO UPDATE SET manifest_json=excluded.manifest_json, checksum=excluded.checksum`, manifest.ID, manifest.Version, string(body), sum, now); err != nil {
		return err
	}
	activeVersion := manifest.Version
	status := manifest.Status
	var existing Record
	err = tx.QueryRowContext(ctx, `SELECT active_version,status FROM skills WHERE id=?`, manifest.ID).Scan(&existing.ActiveVersion, &existing.Status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && manifest.Status != StatusActive {
		activeVersion = existing.ActiveVersion
		status = existing.Status
	}
	if activeVersion == "" {
		activeVersion = manifest.Version
	}
	if status == "" {
		status = StatusDraft
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skills (id,name,description,active_version,status,source,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, active_version=excluded.active_version, status=excluded.status, source=excluded.source, updated_at=excluded.updated_at`, manifest.ID, manifest.Name, manifest.Description, activeVersion, status, source, now, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,description,active_version,status,source,created_at,updated_at FROM skills ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var item Record
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.ActiveVersion, &item.Status, &item.Source, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s Store) Get(ctx context.Context, id string) (Record, error) {
	var item Record
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,description,active_version,status,source,created_at,updated_at FROM skills WHERE id=?`, id).Scan(&item.ID, &item.Name, &item.Description, &item.ActiveVersion, &item.Status, &item.Source, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return item, err
}

func (s Store) GetManifest(ctx context.Context, id string, version string) (Manifest, error) {
	if version == "" {
		record, err := s.Get(ctx, id)
		if err != nil {
			return Manifest{}, err
		}
		version = record.ActiveVersion
	}
	item, err := s.GetVersion(ctx, id, version)
	if err != nil {
		return Manifest{}, err
	}
	return item.Manifest, nil
}

func (s Store) GetVersion(ctx context.Context, id string, version string) (VersionRecord, error) {
	var body string
	var item VersionRecord
	err := s.DB.QueryRowContext(ctx, `SELECT manifest_json,checksum,created_at FROM skill_versions WHERE skill_id=? AND version=?`, id, version).Scan(&body, &item.Checksum, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return VersionRecord{}, ErrNotFound
	}
	if err != nil {
		return VersionRecord{}, err
	}
	if err := json.Unmarshal([]byte(body), &item.Manifest); err != nil {
		return VersionRecord{}, err
	}
	item.Manifest = Normalize(item.Manifest)
	return item, nil
}

func (s Store) Versions(ctx context.Context, id string) ([]VersionRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT manifest_json,checksum,created_at FROM skill_versions WHERE skill_id=? ORDER BY created_at DESC, version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VersionRecord
	for rows.Next() {
		var body string
		var item VersionRecord
		if err := rows.Scan(&body, &item.Checksum, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &item.Manifest); err != nil {
			return nil, err
		}
		item.Manifest = Normalize(item.Manifest)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s Store) Enable(ctx context.Context, id string, version string) error {
	if version == "" {
		versions, err := s.Versions(ctx, id)
		if err != nil {
			return err
		}
		version = versions[0].Manifest.Version
	}
	item, err := s.GetVersion(ctx, id, version)
	if err != nil {
		return err
	}
	item.Manifest.Status = StatusActive
	return s.updateStatus(ctx, item.Manifest, StatusActive)
}

func (s Store) Disable(ctx context.Context, id string) error {
	record, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	manifest, err := s.GetManifest(ctx, id, record.ActiveVersion)
	if err != nil {
		return err
	}
	manifest.Status = StatusDisabled
	return s.updateStatus(ctx, manifest, StatusDisabled)
}

func (s Store) updateStatus(ctx context.Context, manifest Manifest, status Status) error {
	body, err := json.Marshal(Normalize(manifest))
	if err != nil {
		return err
	}
	sum, err := Checksum(manifest)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE skill_versions SET manifest_json=?, checksum=? WHERE skill_id=? AND version=?`, string(body), sum, manifest.ID, manifest.Version); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE skills SET active_version=?, status=?, updated_at=? WHERE id=?`, manifest.Version, status, now, manifest.ID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
