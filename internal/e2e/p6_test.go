package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent/internal/api"
	"agent/internal/browser"
	"agent/internal/config"
	"agent/internal/cost"
	"agent/internal/memory"
	"agent/internal/model"
	"agent/internal/orchestrator"
	"agent/internal/permission"
	"agent/internal/privacy"
	"agent/internal/rag"
	"agent/internal/storage"
	"agent/internal/verification"
)

func TestP6LocalFirstIntegratedFlow(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	confirmations := permission.NewInMemoryConfirmationStore()
	events := &orchestrator.InMemoryEvidenceBus{}
	handler := api.NewServer(db, events, confirmations, "local-planner").Handler()

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"input":"find P6 acceptance notes token=hidden","long":true}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	assertNoSecretLeak(t, create.Body.String())
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.Status != "running" {
		t.Fatalf("created task = %+v, want running task with id", created)
	}

	ragStore := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 24})
	if _, err := ragStore.Index(ctx, rag.Document{
		ID:           "doc-p6",
		SourceURI:    "local://requirements/p6",
		Title:        "P6 acceptance",
		Text:         "P6 local flow uses mock providers SQLite RAG memory browser denial verification and final synthesis.",
		PrivacyClass: "local_private",
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	search := rag.NewBM25(ragStore)
	results, err := search.Search(ctx, "P6 mock providers final synthesis", 3)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search() returned no results")
	}

	episodic := memory.NewStore(db.SQL)
	if err := episodic.Append(ctx, memory.EpisodicEvent{
		ID:          "mem-1",
		TaskID:      created.ID,
		EventType:   "retrieval",
		Summary:     "retrieved P6 acceptance evidence",
		EvidenceIDs: []string{results[0].EvidenceID},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	semantic := memory.NewSemanticStore(db.SQL, ragStore)
	if err := semantic.Upsert(ctx, memory.SemanticFact{
		ID:          "fact-1",
		Scope:       "project",
		Subject:     "P6",
		Predicate:   "uses",
		Object:      "mock providers and local storage",
		Source:      "test",
		EvidenceIDs: []string{results[0].EvidenceID},
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	registry, err := model.NewRegistry(config.ModelsConfig{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: "mock", TrustLevel: string(model.TrustLocalPrivate)},
		},
		Registry: []config.ModelConfig{{
			ID:           "local-planner",
			Provider:     "local",
			Model:        "mock-local-planner",
			TrustLevel:   string(model.TrustLocalPrivate),
			Capabilities: []string{string(model.CapabilityChat)},
			Pricing:      config.PricingConfig{InputPer1M: 1, OutputPer1M: 2},
		}},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	modelMeta, err := registry.Model("local-planner")
	if err != nil {
		t.Fatalf("Model() error = %v", err)
	}
	chat := mockChatProvider{}
	modelResponse, err := chat.Chat(ctx, model.ChatRequest{
		Model: modelMeta,
		Messages: []model.ChatMessage{{
			Role:    "user",
			Content: "P6 local flow uses mock providers and final synthesis.",
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	usage, err := cost.Estimator{Prices: cost.NewPriceBookFromRegistry(registry)}.Estimate(cost.Usage{
		TaskID:       created.ID,
		AgentRole:    "leader",
		ProviderID:   "local",
		ModelID:      "local-planner",
		Operation:    "chat",
		InputTokens:  modelResponse.Usage.InputTokens,
		OutputTokens: modelResponse.Usage.OutputTokens,
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	limit := cost.Enforcer{}.Check(cost.Budget{HardLimitUSD: 1}, usage)
	if limit.Status != cost.LimitOK {
		t.Fatalf("cost limit = %+v, want ok", limit)
	}

	report := verification.Verifier{}.Verify([]verification.Claim{{
		ID:          "claim-1",
		Text:        modelResponse.Content,
		Confidence:  0.95,
		EvidenceIDs: []string{"ev-1"},
	}}, []verification.Evidence{{
		ID:       "ev-1",
		Text:     results[0].Chunk.Text,
		Supports: []string{"P6 local flow uses mock providers"},
	}})
	if !report.PassesPolicy {
		t.Fatalf("verification report = %+v, want pass", report)
	}

	runtime := &recordingBrowserRuntime{}
	browserBus := &browser.InMemoryBrowserEvidenceBus{}
	sessions := browser.NewInMemorySessionManager()
	if _, err := sessions.Create(browser.Session{
		ID:              "session-1",
		Domains:         []string{"example.test"},
		ExpiresAt:       time.Now().UTC().Add(time.Hour),
		ReusePolicy:     browser.ReuseWhenAllowed,
		IsolationPolicy: browser.IsolationPerTask,
		StoragePolicy:   browser.StorageLocalOnly,
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	tool := browser.GovernedTool{
		Sessions: sessions,
		Classifier: browser.PermissionClassifier{Policy: browser.PermissionPolicy{
			AutoAllowRead:            true,
			RequireAllowlistedDomain: true,
		}},
		Runtime:     runtime,
		Evidence:    browser.EvidenceBuilder{Privacy: browser.PrivacyGateway{}},
		EvidenceBus: browserBus,
	}
	browserResult, err := tool.Execute(ctx, browser.Action{
		Type:      browser.ActionDeleteData,
		TaskID:    created.ID,
		AgentID:   "leader",
		SessionID: "session-1",
		Domain:    "example.test",
		URL:       "https://example.test/delete?token=hidden",
		Target:    browser.Target{Description: "delete account token=hidden"},
	})
	if err != nil {
		t.Fatalf("browser Execute() error = %v", err)
	}
	if browserResult.Decision != browser.DecisionRequiresExplicit {
		t.Fatalf("browser decision = %s, want explicit confirmation", browserResult.Decision)
	}
	if runtime.called {
		t.Fatal("browser runtime was called for unconfirmed high-risk action")
	}
	for _, event := range browserBus.Events() {
		assertNoSecretLeak(t, event.Domain)
		assertNoSecretLeak(t, event.Target.Description)
	}

	if err := events.Publish(ctx, orchestrator.Event{
		Type:        orchestrator.EventNodeCompleted,
		TaskID:      created.ID,
		NodeID:      "synthesis",
		EvidenceIDs: []string{results[0].EvidenceID},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	eventResponse := httptest.NewRecorder()
	handler.ServeHTTP(eventResponse, httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID+"/events", nil))
	if eventResponse.Code != http.StatusOK || !strings.Contains(eventResponse.Body.String(), string(orchestrator.EventNodeCompleted)) {
		t.Fatalf("event response status=%d body=%s", eventResponse.Code, eventResponse.Body.String())
	}
}

func TestP6SecretLeakRegressionFixtures(t *testing.T) {
	gateway, err := privacy.NewGateway("test-secret")
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	if _, err := gateway.TransformText("PASSWORD=supersecret\nTOKEN=hidden"); err != privacy.ErrPayloadBlocked {
		t.Fatalf("TransformText() error = %v, want payload blocked", err)
	}
	result, err := gateway.TransformText("send authorization: bearer abc123 to user@example.com")
	if err != nil {
		t.Fatalf("TransformText() redaction error = %v", err)
	}
	assertNoSecretLeak(t, result.Text)
	if strings.Contains(result.Text, "user@example.com") {
		t.Fatalf("pseudonymized text leaked email: %s", result.Text)
	}

	browserEvent := browser.EvidenceBuilder{Privacy: browser.PrivacyGateway{}}.Build(
		browser.EvidenceActionExecuted,
		"task-1",
		"agent-1",
		browser.Result{
			Action: browser.Action{
				Domain: "example.test",
				Target: browser.Target{
					Description: "authorization: bearer abc123 password=supersecret",
				},
			},
			Observation: browser.Observation{
				VisibleText: "token=hidden user@example.com",
			},
		},
		false,
	)
	assertNoSecretLeak(t, browserEvent.Target.Description)
	assertNoSecretLeak(t, browserEvent.Observation.VisibleText)
}

func TestP6PostgresIntegrationSkippedWhenUnavailable(t *testing.T) {
	if os.Getenv("PACHAT_POSTGRES_TEST_DSN") == "" {
		t.Skip("PACHAT_POSTGRES_TEST_DSN is not set")
	}
	t.Skip("PostgreSQL connector is a P0 mockable boundary; enable this when storage.Open supports postgres")
}

type mockChatProvider struct{}

func (mockChatProvider) Chat(ctx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ChatResponse{}, err
	}
	return model.ChatResponse{
		Content: "P6 local flow uses mock providers",
		Usage: model.Usage{
			InputTokens:  len(strings.Fields(request.Messages[0].Content)),
			OutputTokens: 6,
		},
	}, nil
}

type recordingBrowserRuntime struct {
	called bool
}

func (r *recordingBrowserRuntime) Execute(context.Context, browser.Action) (browser.Observation, error) {
	r.called = true
	return browser.Observation{Title: "should not execute"}, nil
}

func openDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func assertNoSecretLeak(t *testing.T, text string) {
	t.Helper()
	for _, forbidden := range []string{"supersecret", "token=hidden", "password=", "authorization: bearer", "abc123"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("secret-like value leaked in %q", text)
		}
	}
}
