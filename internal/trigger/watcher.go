package trigger

import (
	"context"
	"os"
	"path/filepath"
	"time"

	agentEvent "agent/internal/event"
)

type FileWatcher struct {
	Events agentEvent.Store
	Now    func() time.Time
}

func (w FileWatcher) Scan(ctx context.Context, projectID string, root string) (int, error) {
	if projectID == "" {
		projectID = "default"
	}
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		id := "fs_" + ExecutionKey(path, info.ModTime(), "")
		event := agentEvent.Event{
			ID:           id,
			Source:       "filesystem",
			Type:         "file_seen",
			ProjectID:    projectID,
			Payload:      []byte(path),
			PrivacyClass: "local_private",
			TrustLevel:   "untrusted",
			OccurredAt:   info.ModTime(),
			ReceivedAt:   now,
			DedupKey:     id,
		}
		if putErr := w.Events.Put(ctx, event); putErr == nil {
			count++
		}
		return nil
	})
	return count, err
}
