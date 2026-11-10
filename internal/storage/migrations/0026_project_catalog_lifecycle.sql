-- Catalog organization only. Never a policy/execution/knowledge authority.
-- Retain tombstones so delete/recreate changes both generation and revision.
CREATE TABLE project_catalog_lifecycle (
 project_id TEXT PRIMARY KEY,
 generation TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision > 0),
 state TEXT NOT NULL CHECK(state IN ('active','archived'))
);
INSERT INTO project_catalog_lifecycle SELECT id,lower(hex(randomblob(16))),1,'active' FROM projects;
CREATE TRIGGER project_catalog_insert AFTER INSERT ON projects BEGIN
 INSERT INTO project_catalog_lifecycle(project_id,generation,revision,state)
 VALUES(NEW.id,lower(hex(randomblob(16))),1,'active')
 ON CONFLICT(project_id) DO UPDATE SET generation=excluded.generation,revision=project_catalog_lifecycle.revision+1,state='active';
END;
CREATE TRIGGER project_catalog_rename AFTER UPDATE OF name ON projects
WHEN CAST(OLD.name AS BLOB) != CAST(NEW.name AS BLOB) BEGIN
 UPDATE project_catalog_lifecycle SET revision=revision+1 WHERE project_id=NEW.id;
END;
