package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"agent/internal/capability"
	"agent/internal/permission"
	"agent/internal/project"
	"agent/internal/runtime"
	"agent/internal/skill"
	"agent/internal/storage"
	"agent/internal/workflow"
)

type dashboardData struct {
	Status        string                  `json:"status"`
	LeaderModel   string                  `json:"leader_model"`
	Models        []string                `json:"models"`
	Tasks         []storage.Task          `json:"tasks"`
	Workflows     []workflow.Run          `json:"workflows"`
	Approvals     []permission.Request    `json:"approvals"`
	Notifications []dashboardNotification `json:"notifications"`
	Projects      []project.Project       `json:"projects"`
	Skills        []skill.Record          `json:"skills"`
	Capabilities  []capability.Capability `json:"capabilities"`
	Triggers      []dashboardTrigger      `json:"triggers"`
	Summary       map[string]int          `json:"summary"`
}

type dashboardNotification struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Severity      string `json:"severity"`
	Status        string `json:"status"`
	DeliveryState string `json:"delivery_state"`
	CreatedAt     string `json:"created_at"`
}

type dashboardTrigger struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Type      string `json:"type"`
	Enabled   bool   `json:"enabled"`
	SkillID   string `json:"skill_id"`
}

type dashboardSSEEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data any    `json:"data"`
}

func (s Server) dashboard(w http.ResponseWriter, r *http.Request) {
	data := s.collectDashboardData(r.Context())
	raw, err := json.Marshal(data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "dashboard_data_failed")
		return
	}
	page := struct {
		Data template.JS
	}{
		Data: template.JS(strings.ReplaceAll(string(raw), "</", "<\\/")),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = dashboardTemplate.Execute(w, page)
}

func (s Server) collectDashboardData(ctx context.Context) dashboardData {
	data := dashboardData{
		Status:       "ok",
		LeaderModel:  s.LeaderModelID,
		Models:       append([]string(nil), s.ModelRegistry...),
		Summary:      map[string]int{},
		Capabilities: []capability.Capability{},
	}
	if tasks, err := s.Tasks.ListTasks(ctx, 20); err == nil {
		data.Tasks = tasks
		for _, task := range tasks {
			data.Summary["tasks_"+task.Status]++
		}
		data.Summary["tasks"] = len(tasks)
	}
	if db := s.sqlDB(); db != nil {
		if runs, err := (workflow.Store{DB: db}).List(ctx, 20); err == nil {
			data.Workflows = runs
			data.Summary["workflows"] = len(runs)
		}
		if projects, err := (project.Store{DB: db}).List(ctx, 20); err == nil {
			data.Projects = projects
			data.Summary["projects"] = len(projects)
		}
		if skills, err := (skill.Store{DB: db}).List(ctx); err == nil {
			data.Skills = skills
			data.Summary["skills"] = len(skills)
		}
	}
	if lister, ok := s.Confirmations.(interface {
		List(context.Context, string, int) ([]permission.Request, error)
	}); ok {
		if approvals, err := lister.List(ctx, "pending", 20); err == nil {
			data.Approvals = approvals
			data.Summary["approvals"] = len(approvals)
		}
	}
	if s.Notifications.DB != nil {
		if notifications, err := s.Notifications.List(ctx, 20); err == nil {
			for _, item := range notifications {
				data.Notifications = append(data.Notifications, dashboardNotification{
					ID:            item.ID,
					Title:         item.Title,
					Severity:      item.Severity,
					Status:        item.Status,
					DeliveryState: item.DeliveryState,
					CreatedAt:     item.CreatedAt.Format(time.RFC3339),
				})
			}
			data.Summary["notifications"] = len(data.Notifications)
		}
	}
	if s.Triggers.DB != nil {
		if triggers, err := s.Triggers.List(ctx); err == nil {
			for _, item := range triggers {
				data.Triggers = append(data.Triggers, dashboardTrigger{
					ID:        item.ID,
					ProjectID: item.ProjectID,
					Type:      string(item.Type),
					Enabled:   item.Enabled,
					SkillID:   item.SkillID,
				})
			}
			data.Summary["triggers"] = len(data.Triggers)
		}
	}
	if rt, ok := s.Runner.(*runtime.Runtime); ok && rt.Capabilities != nil {
		data.Capabilities = rt.Capabilities.List("")
		data.Summary["capabilities"] = len(data.Capabilities)
	}
	return data
}

func (s Server) sqlDB() *sql.DB {
	if db, ok := s.Tasks.(*storage.DB); ok {
		return db.SQL
	}
	return nil
}

func (s Server) dashboardEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	lastID := r.Header.Get("Last-Event-ID")
	for _, event := range s.collectDashboardEvents(r.Context(), lastID) {
		writeDashboardSSE(w, event)
	}
	writeDashboardSSE(w, dashboardSSEEvent{ID: eventCursor(time.Now().UTC(), "ready", "ready"), Type: "ready", Data: map[string]string{"status": "ok"}})
	if flusher != nil {
		flusher.Flush()
	}
	if r.URL.Query().Get("once") == "1" {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case now := <-ticker.C:
			writeDashboardSSE(w, dashboardSSEEvent{ID: eventCursor(now.UTC(), "ready", "heartbeat"), Type: "ready", Data: map[string]string{"status": "heartbeat"}})
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (s Server) collectDashboardEvents(ctx context.Context, lastID string) []dashboardSSEEvent {
	events := []dashboardSSEEvent{}
	add := func(at time.Time, kind string, id string, data any) {
		cursor := eventCursor(at, kind, id)
		if lastID != "" && cursor <= lastID {
			return
		}
		events = append(events, dashboardSSEEvent{ID: cursor, Type: kind, Data: data})
	}
	if tasks, err := s.Tasks.ListTasks(ctx, 50); err == nil {
		for _, item := range tasks {
			add(item.UpdatedAt, "task", item.ID, taskToResponse(item))
		}
	}
	if db := s.sqlDB(); db != nil {
		if runs, err := (workflow.Store{DB: db}).List(ctx, 50); err == nil {
			for _, item := range runs {
				add(item.UpdatedAt, "workflow", item.ID, item)
			}
		}
	}
	if lister, ok := s.Confirmations.(interface {
		List(context.Context, string, int) ([]permission.Request, error)
	}); ok {
		if approvals, err := lister.List(ctx, "", 50); err == nil {
			for _, item := range approvals {
				status := "pending"
				if statuses, ok := s.Confirmations.(ConfirmationStatusStore); ok {
					if got, err := statuses.Status(ctx, item.ID); err == nil && got != "" {
						status = got
					}
				}
				add(item.RequestedAt, "approval", item.ID, confirmationToResponse(item, status))
			}
		}
	}
	if s.Notifications.DB != nil {
		if notifications, err := s.Notifications.List(ctx, 50); err == nil {
			for _, item := range notifications {
				add(item.CreatedAt, "notification", item.ID, dashboardNotification{ID: item.ID, Title: item.Title, Severity: item.Severity, Status: item.Status, DeliveryState: item.DeliveryState, CreatedAt: item.CreatedAt.Format(time.RFC3339)})
			}
		}
	}
	if s.Triggers.DB != nil {
		if triggers, err := s.Triggers.List(ctx); err == nil {
			for _, item := range triggers {
				add(item.UpdatedAt, "trigger", item.ID, dashboardTrigger{ID: item.ID, ProjectID: item.ProjectID, Type: string(item.Type), Enabled: item.Enabled, SkillID: item.SkillID})
			}
		}
	}
	return events
}

func writeDashboardSSE(w io.Writer, event dashboardSSEEvent) {
	raw, _ := json.Marshal(event.Data)
	_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, raw)
}

func eventCursor(at time.Time, kind string, id string) string {
	if at.IsZero() {
		at = time.Unix(0, 0).UTC()
	}
	return fmt.Sprintf("%020d:%s:%s", at.UTC().UnixNano(), kind, id)
}

var dashboardTemplate = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Personal Agent Operator</title>
<style>
:root{color-scheme:light dark;--bg:#f7f8f5;--panel:#ffffff;--panel-2:#f0f4ef;--text:#18201d;--muted:#65716c;--line:#d9dfd8;--accent:#126b57;--accent-2:#b45309;--danger:#b42318;--ok:#16833a;--shadow:0 18px 50px rgba(24,32,29,.08)}
@media (prefers-color-scheme:dark){:root{--bg:#101411;--panel:#171d19;--panel-2:#202820;--text:#edf3ed;--muted:#9eaaa3;--line:#303a33;--accent:#5ee0bf;--accent-2:#f7b267;--danger:#ff8a80;--ok:#7bd88f;--shadow:0 18px 50px rgba(0,0,0,.28)}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font-family:"Avenir Next","Segoe UI",sans-serif;font-size:14px;line-height:1.45}button,input,textarea{font:inherit}button{border:1px solid var(--line);background:var(--panel);color:var(--text);border-radius:6px;padding:8px 10px;cursor:pointer}button.primary{background:var(--accent);border-color:var(--accent);color:#fff}button:focus,input:focus,textarea:focus{outline:2px solid var(--accent);outline-offset:2px}.shell{display:grid;grid-template-columns:220px minmax(0,1fr);min-height:100vh}.nav{border-right:1px solid var(--line);padding:18px 14px;background:var(--panel)}.brand{font-weight:700;font-size:18px;margin-bottom:18px}.nav button{width:100%;text-align:left;margin:2px 0;background:transparent}.nav button.active{background:var(--panel-2);border-color:var(--accent);color:var(--accent)}main{padding:18px 22px 28px}.top{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:16px}.top h1{font-size:22px;margin:0}.top p{margin:4px 0 0;color:var(--muted)}.grid{display:grid;gap:12px}.stats{grid-template-columns:repeat(6,minmax(110px,1fr));margin-bottom:14px}.stat{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:12px;box-shadow:var(--shadow)}.stat span{display:block;color:var(--muted);font-size:12px}.stat strong{display:block;font-size:24px;margin-top:2px}.workbench{display:grid;grid-template-columns:minmax(0,1fr) 340px;gap:14px}.panel{background:var(--panel);border:1px solid var(--line);border-radius:8px;box-shadow:var(--shadow);overflow:hidden}.panel header{display:flex;justify-content:space-between;align-items:center;gap:12px;padding:12px 14px;border-bottom:1px solid var(--line);background:var(--panel-2)}.panel h2{font-size:14px;margin:0}.content{padding:12px 14px}.run{display:grid;gap:10px}.run textarea{width:100%;min-height:108px;resize:vertical;border:1px solid var(--line);border-radius:8px;background:var(--panel);color:var(--text);padding:10px}.run .row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:10px}.run input{border:1px solid var(--line);border-radius:6px;background:var(--panel);color:var(--text);padding:8px 10px}.table{width:100%;border-collapse:collapse}.table th,.table td{padding:9px 8px;border-bottom:1px solid var(--line);text-align:left;vertical-align:top}.table th{font-size:12px;color:var(--muted);font-weight:600;background:var(--panel-2)}.table td{font-size:13px}.tag{display:inline-flex;align-items:center;border:1px solid var(--line);border-radius:999px;padding:2px 7px;font-size:12px;color:var(--muted);white-space:nowrap}.tag.ok{color:var(--ok);border-color:color-mix(in srgb,var(--ok),var(--line) 70%)}.tag.warn{color:var(--accent-2);border-color:color-mix(in srgb,var(--accent-2),var(--line) 70%)}.tag.danger{color:var(--danger);border-color:color-mix(in srgb,var(--danger),var(--line) 70%)}.empty{color:var(--muted);padding:18px 0}.split{display:grid;grid-template-columns:1fr 1fr;gap:12px}.tabs{display:none}.tabs.active{display:block}.mono{font-family:"SFMono-Regular","Cascadia Code",monospace;font-size:12px}.toast{min-height:20px;color:var(--muted)}@media (max-width:980px){.shell{grid-template-columns:1fr}.nav{position:sticky;top:0;z-index:2;border-right:0;border-bottom:1px solid var(--line)}.nav .links{display:grid;grid-template-columns:repeat(4,1fr);gap:6px}.workbench,.split{grid-template-columns:1fr}.stats{grid-template-columns:repeat(2,1fr)}main{padding:14px}}@media (max-width:560px){.nav .links{grid-template-columns:repeat(2,1fr)}.top{display:block}.run .row{grid-template-columns:1fr}.table{display:block;overflow-x:auto;white-space:nowrap}}
</style>
</head>
<body>
<div class="shell">
<aside class="nav"><div class="brand">Personal Agent</div><div class="links" id="nav"></div></aside>
<main>
<div class="top"><div><h1>Operator Console</h1><p id="subtitle"></p></div><button id="refresh">Refresh</button></div>
<section class="stats grid" id="stats"></section>
<section class="workbench">
<div class="panel"><header><h2 id="view-title">Chat / Run</h2><span class="tag" id="view-count">ready</span></header><div class="content" id="view"></div></div>
<aside class="grid">
<div class="panel"><header><h2>Events</h2><span class="tag" id="event-state">connecting</span></header><div class="content"><div class="toast mono" id="event-log">waiting</div></div></div>
<div class="panel"><header><h2>Approvals</h2><span class="tag warn" id="approval-count">0</span></header><div class="content" id="approvals"></div></div>
<div class="panel"><header><h2>Notifications</h2><span class="tag" id="notification-count">0</span></header><div class="content" id="notifications"></div></div>
</aside>
</section>
</main>
</div>
<script id="dashboard-data" type="application/json">{{.Data}}</script>
<script>
let data=JSON.parse(document.getElementById("dashboard-data").textContent);let current="run";
const views=[["run","Chat / Run"],["tasks","Tasks"],["workflows","Workflows"],["projects","Projects"],["skills","Skills"],["capabilities","Capabilities Health"],["triggers","Triggers"]];
const el=(id)=>document.getElementById(id);const esc=(v)=>String(v??"").replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]));
function tag(v){let c=["failed","denied","unavailable"].includes(String(v))?"danger":["pending","running","waiting_approval","unknown"].includes(String(v))?"warn":"ok";return "<span class=\"tag "+c+"\">"+esc(v||"ok")+"</span>"}
function drawNav(){el("nav").innerHTML=views.map(([id,label])=>"<button data-view=\""+id+"\" class=\""+(id===current?"active":"")+"\">"+label+"</button>").join("");document.querySelectorAll("[data-view]").forEach(b=>b.onclick=()=>{current=b.dataset.view;render()})}
function drawStats(){let s=data.summary||{};let stats=[["Tasks",s.tasks||0],["Running",s.tasks_running||0],["Workflows",s.workflows||0],["Approvals",s.approvals||0],["Skills",s.skills||0],["Triggers",s.triggers||0]];el("stats").innerHTML=stats.map(([k,v])=>"<div class=\"stat\"><span>"+k+"</span><strong>"+v+"</strong></div>").join("")}
function table(cols,rows){if(!rows||!rows.length)return "<div class=\"empty\">No records</div>";return "<table class=\"table\"><thead><tr>"+cols.map(c=>"<th>"+c[0]+"</th>").join("")+"</tr></thead><tbody>"+rows.map(r=>"<tr>"+cols.map(c=>"<td>"+(c[2]?c[2](r):esc(r[c[1]]))+"</td>").join("")+"</tr>").join("")+"</tbody></table>"}
function runView(){return "<form class=\"run\" id=\"run-form\"><textarea name=\"input\" placeholder=\"Ask the local agent\"></textarea><div class=\"row\"><input name=\"project_id\" placeholder=\"project_id\"><button class=\"primary\">Run</button></div><div class=\"toast\" id=\"run-result\"></div></form>"}
function renderView(){let v=current;if(v==="run")return runView();if(v==="tasks")return table([["ID","id",r=>"<span class=\"mono\">"+esc(r.id)+"</span>"],["Title","title"],["Status","status",r=>tag(r.status)],["Model","leader_model_id"],["Updated","updated_at"]],data.tasks);if(v==="workflows")return table([["ID","id",r=>"<span class=\"mono\">"+esc(r.id)+"</span>"],["Task","task_id"],["Status","status",r=>tag(r.status)],["Skill","skill_id"],["Updated","updated_at"]],data.workflows);if(v==="projects")return table([["ID","id"],["Name","name"],["Privacy","privacy_class"],["Budget","budget_policy"]],data.projects);if(v==="skills")return table([["ID","id"],["Version","active_version"],["Status","status",r=>tag(r.status)],["Name","name"]],data.skills);if(v==="capabilities")return table([["ID","id"],["Kind","kind"],["Health","health",r=>tag(r.health)],["Enabled","enabled",r=>tag(r.enabled?"enabled":"disabled")]],data.capabilities);if(v==="triggers")return table([["ID","id"],["Type","type"],["Enabled","enabled",r=>tag(r.enabled?"enabled":"disabled")],["Project","project_id"],["Skill","skill_id"]],data.triggers);return ""}
function side(){el("approval-count").textContent=(data.approvals||[]).length;el("notification-count").textContent=(data.notifications||[]).length;el("approvals").innerHTML=table([["ID","id"],["Action","action"],["Risk","risk",r=>tag(r.risk)]],data.approvals);el("notifications").innerHTML=table([["Title","title"],["Severity","severity",r=>tag(r.severity)],["State","delivery_state"]],data.notifications)}
function render(){drawNav();drawStats();let active=views.find(v=>v[0]===current)||views[0];el("view-title").textContent=active[1];el("view").innerHTML=renderView();el("view-count").textContent=current==="run"?"ready":((data[current]||[]).length+" rows");el("subtitle").textContent="leader="+(data.leader_model||"local")+" models="+((data.models||[]).join(",")||"none");side();let form=el("run-form");if(form)form.onsubmit=submitRun}
async function submitRun(e){e.preventDefault();let fd=new FormData(e.currentTarget);let body={input:fd.get("input"),project_id:fd.get("project_id")};el("run-result").textContent="running";let res=await fetch("/tasks",{method:"POST",headers:{"content-type":"application/json"},body:JSON.stringify(body)});let out=await res.json();el("run-result").textContent=res.ok?("task "+out.id+" "+out.status):("error "+(out.error||res.status));await refresh()}
async function refresh(){for(let item of [["tasks","/tasks?limit=20"],["notifications","/notifications"],["triggers","/triggers"],["models","/models/discover"]]){try{let res=await fetch(item[1]);if(res.ok){let json=await res.json();data[item[0]]=json[item[0]]||data[item[0]]}}catch(e){}}render()}
function connectEvents(){if(!window.EventSource)return;let source=new EventSource("/dashboard/events");source.onopen=()=>{el("event-state").textContent="live"};source.onerror=()=>{el("event-state").textContent="retrying"};["task","workflow","approval","notification","trigger","ready"].forEach(name=>source.addEventListener(name,e=>{el("event-log").textContent=name+" "+(e.lastEventId||"");try{if(name==="task")refresh()}catch(err){}}))}
el("refresh").onclick=refresh;render();connectEvents();
</script>
</body>
</html>`))
