CREATE TABLE interaction_chat_policies(session_id TEXT PRIMARY KEY REFERENCES interaction_sessions(id), policy TEXT NOT NULL);
CREATE TRIGGER interaction_chat_immutable BEFORE UPDATE ON interaction_chat_policies BEGIN SELECT RAISE(ABORT,'immutable chat policy'); END;
CREATE TRIGGER interaction_chat_no_delete BEFORE DELETE ON interaction_chat_policies BEGIN SELECT RAISE(ABORT,'immutable chat policy'); END;
CREATE TRIGGER interaction_chat_exclusive BEFORE INSERT ON interaction_chat_policies WHEN EXISTS(SELECT 1 FROM interaction_workspaces WHERE session_id=NEW.session_id) OR EXISTS(SELECT 1 FROM interaction_runs WHERE session_id=NEW.session_id) BEGIN SELECT RAISE(ABORT,'session policy already bound'); END;
CREATE TRIGGER interaction_workspace_exclusive BEFORE INSERT ON interaction_workspaces WHEN EXISTS(SELECT 1 FROM interaction_chat_policies WHERE session_id=NEW.session_id) BEGIN SELECT RAISE(ABORT,'chat-only policy'); END;
CREATE TABLE interaction_api_sessions(session_id TEXT PRIMARY KEY REFERENCES interaction_sessions(id), request_id TEXT UNIQUE NOT NULL, binding TEXT NOT NULL, mode TEXT NOT NULL);
CREATE TRIGGER interaction_api_binding_immutable BEFORE UPDATE ON interaction_api_sessions BEGIN SELECT RAISE(ABORT,'immutable provider binding'); END;
CREATE TABLE interaction_api_actions(request_id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, result TEXT NOT NULL);
