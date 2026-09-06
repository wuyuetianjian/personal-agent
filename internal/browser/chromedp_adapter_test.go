package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

func TestChromedpRuntimeLocalE2E(t *testing.T) {
	if os.Getenv("PACHAT_BROWSER_E2E") != "1" {
		t.Skip("set PACHAT_BROWSER_E2E=1 to run local browser E2E")
	}

	server := httptest.NewServer(http.FileServer(http.Dir("testdata/browser")))
	defer server.Close()

	runtime, err := NewChromedpRuntime(context.Background(), ChromedpOptions{
		AllocatorOpts: []chromedp.ExecAllocatorOption{
			chromedp.Headless,
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
		},
		ScreenshotDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewChromedpRuntime() error = %v", err)
	}
	defer runtime.Close()

	obs, err := runtime.Navigate(context.Background(), server.URL+"/index.html")
	if err != nil {
		t.Fatalf("Navigate() error = %v", err)
	}
	if obs.Title != "P4 Browser Fixture" {
		t.Fatalf("Title = %q, want fixture", obs.Title)
	}

	obs, err = runtime.Click(context.Background(), Target{Selector: "#increment"})
	if err != nil {
		t.Fatalf("Click() error = %v", err)
	}
	if !strings.Contains(obs.VisibleText, "Count 1") {
		t.Fatalf("VisibleText = %q, want Count 1", obs.VisibleText)
	}

	obs, err = runtime.Screenshot(context.Background())
	if err != nil {
		t.Fatalf("Screenshot() error = %v", err)
	}
	if _, err := os.Stat(obs.ScreenshotID); err != nil {
		t.Fatalf("screenshot file stat error = %v", err)
	}
}
