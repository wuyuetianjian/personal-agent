ALTER TABLE projects ADD COLUMN allowed_models_json text not null DEFAULT '[]';
