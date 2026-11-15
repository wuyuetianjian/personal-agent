package api

import (
	"agent/internal/interaction"
	"context"
	"errors"
	"testing"
)

// completeTrustedFixtureRun ends only the manually driven test run. Closing a
// service/store is not a completion signal for its persisted running run.
func completeTrustedFixtureRun(t *testing.T, ctx context.Context, store *interaction.Store, run interaction.Run) {
	t.Helper()
	current, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.Transition(ctx, current.ID, current.Version, interaction.RunCompleted)
	if err != nil || completed.State != interaction.RunCompleted || completed.Version != current.Version+1 {
		t.Fatalf("complete manually driven fixture run: %+v %v", completed, err)
	}
}

func TestTrustedWorkspaceFixtureRunLifecycle(t *testing.T) {
	ctx := context.Background()
	rt, err := OpenInteractionRuntime(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	store := interaction.NewStore(rt.DB.SQL)
	if err := store.CreateSession(ctx, "fixture-lifecycle"); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"baseline", "host-off", "root-changed", "host-restored"} {
		store = interaction.NewStore(rt.DB.SQL)
		run, err := store.CreateRun(ctx, "fixture-lifecycle", phase, interaction.Message{Content: "fixture lifecycle only"})
		if err != nil {
			t.Fatalf("%s next run: %v", phase, err)
		}
		reloaded := interaction.NewStore(rt.DB.SQL)
		if _, err := reloaded.CreateRun(ctx, "fixture-lifecycle", phase+"-overlap", interaction.Message{}); !errors.Is(err, interaction.ErrConflict) {
			t.Fatalf("%s active run lost exclusivity on reload: %v", phase, err)
		}
		completeTrustedFixtureRun(t, ctx, store, run)
		saved, err := reloaded.GetRun(ctx, run.ID)
		if err != nil || saved.State != interaction.RunCompleted || saved.Version != run.Version+1 {
			t.Fatalf("%s fixture did not persist completion: %+v %v", phase, saved, err)
		}
	}
}
