package main

import (
	"context"
	"database/sql"
	"fmt"
)

// Called only after --operator-fixture has refused caller-supplied paths. All
// records are owned deterministic data, not an importer or production API.
func seedOperatorFixture(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for i := 0; i < 60; i++ {
		if _, e = tx.ExecContext(ctx, `INSERT INTO projects VALUES(?,?,'private','[]','[]','','[]','[]','[]','', '[]')`, fmt.Sprintf("project-%03d", i), "<img src=x onerror=alert(1)>\x1b[31m label"); e != nil {
			return e
		}
	}
	for _, q := range []string{
		`INSERT INTO tasks(id,title,input,status,created_at,updated_at,leader_model_id,privacy_class,error_message) VALUES('owned-task','Fixture task','SECRET_INPUT','completed',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'fixture','private','SECRET_ERROR')`,
		`INSERT INTO evidence VALUES('owned-evidence','owned-task',NULL,'note','SECRET_SOURCE','SECRET_URI','SECRET_CONTENT','hash','SECRET_METADATA','private',CURRENT_TIMESTAMP)`,
		`INSERT INTO workflow_runs VALUES('owned-workflow','owned-task','project-000','local.lookup','v1','completed','SECRET_INPUT','SECRET_RESULT',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO workflow_nodes(workflow_id,node_id,capability_id,role,status,attempt,idempotency_key,side_effect) VALUES('owned-workflow','owned-node','fixture.read','reader','completed',1,'fixture',0)`,
		`INSERT INTO workflow_checkpoints(workflow_id,node_id,status,evidence_ids_json,result_ref,usage_json,created_at) VALUES('owned-workflow','owned-node','completed','["owned-evidence"]','SECRET_REF','{}',CURRENT_TIMESTAMP)`,
		`INSERT INTO skills VALUES('local.lookup','Fixture skill','SECRET_DESCRIPTION','v1','active','SECRET_SOURCE',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO skill_versions VALUES('local.lookup','v1','SECRET_MANIFEST','checksum',CURRENT_TIMESTAMP)`,
		`INSERT INTO triggers VALUES('owned-trigger','project-000','schedule',1,'','local.lookup','{}','{}','{}','','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO trigger_state(trigger_id,last_event_hash,consecutive_failures,state_json) VALUES('owned-trigger','hash',0,'SECRET_STATE')`,
		`INSERT INTO trigger_history VALUES('owned-history','owned-trigger','event','owned-workflow','completed','SECRET_MESSAGE',CURRENT_TIMESTAMP)`,
		`INSERT INTO dead_letter_events VALUES('owned-deadletter','owned-trigger','trigger','SECRET_REASON','SECRET_PAYLOAD',CURRENT_TIMESTAMP)`,
		`INSERT INTO notification_inbox(id,project_id,trigger_id,title,body,dedup_key,status,created_at) VALUES('owned-notification','project-000','owned-trigger','Fixture notice','SECRET_BODY','','unread',CURRENT_TIMESTAMP)`,
		`INSERT INTO security_audit_log VALUES('owned-audit','SECRET_ACTOR','fixture.action','SECRET_TARGET','completed','SECRET_REASON','[]',CURRENT_TIMESTAMP)`,
	} {
		if _, e = tx.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO tasks(id,title,input,status,created_at,updated_at,leader_model_id,privacy_class) VALUES(?,'Bidi identity fixture','','completed',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'fixture','private')`, "task-\u202eevil\u202c-\u2066isolate\u2069"); e != nil {
		return e
	}
	return tx.Commit()
}
