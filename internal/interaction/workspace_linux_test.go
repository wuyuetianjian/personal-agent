//go:build linux

package interaction

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWorkspaceRejectsFIFOWithoutBlocking(t *testing.T) {
	_, _, w, _ := workspaceFixture(t, ModeDefault)
	if err := unix.Mkfifo(filepath.Join(w.Policy.Root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := readWorkspaceFile(ctx, w, "pipe"); err == nil {
		t.Fatal("read special file")
	}
}
func TestWorkspaceDirectorySwapNeverFollowsSymlink(t *testing.T) {
	_, _, w, _ := workspaceFixture(t, ModeDefault)
	if err := os.Mkdir(filepath.Join(w.Policy.Root, "safe"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(w.Policy.Root, "safe", "hop")); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkspaceFile(context.Background(), w, "safe/hop/secret"); err == nil {
		t.Fatal("followed ancestor symlink")
	}
}

func TestWorkspacePersistedRootIdentityIncludesBirthTime(t *testing.T) {
	_, _, w, _ := workspaceFixture(t, ModeDefault)
	// Device/inode alone can be reused after deletion between restarts.
	if len(strings.Split(w.RootIdentity, ":")) != 5 {
		t.Fatal("root identity omits creation time", w.RootIdentity)
	}
}

func TestWorkspaceReadRacingSymlinkSwapNeverEscapes(t *testing.T) {
	_, _, w, _ := workspaceFixture(t, ModeDefault)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("OUTSIDE SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(w.Policy.Root, "racy")
	if err := os.WriteFile(target, []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			link := filepath.Join(w.Policy.Root, "next-link")
			if err := os.Symlink(outside, link); err != nil {
				errCh <- err
				return
			}
			if err := os.Rename(link, target); err != nil {
				errCh <- err
				return
			}
			file := filepath.Join(w.Policy.Root, "next-file")
			if err := os.WriteFile(file, []byte("inside"), 0600); err != nil {
				errCh <- err
				return
			}
			if err := os.Rename(file, target); err != nil {
				errCh <- err
				return
			}
		}
	}()
	for i := 0; i < 200; i++ {
		data, err := readWorkspaceFile(context.Background(), w, "racy")
		if err == nil && string(data) != "inside" {
			t.Errorf("racing path escaped: %q", data)
			break
		}
	}
	<-done
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}

func TestWorkspaceHeadReservationCannotWidenLastByteCheck(t *testing.T) {
	_, _, w, _ := workspaceFixture(t, ModeDefault)
	target := filepath.Join(w.Policy.Root, "note.txt")
	err := replaceWorkspaceFileChecked(context.Background(), w, PatchArguments{Scope: w.ScopeID, Path: "note.txt", Preimage: contentHash([]byte("old")), Content: "inverse"}, func() error { return os.WriteFile(target, []byte("user during reservation"), 0600) })
	if err == nil {
		t.Fatal("head reservation must precede the final current-byte check")
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "user during reservation" {
		t.Fatal("overwrote intervening user modification", string(raw))
	}
}
