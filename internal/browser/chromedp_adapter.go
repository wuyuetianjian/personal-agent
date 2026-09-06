package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

type ChromedpRuntime struct {
	ctx           context.Context
	cancel        context.CancelFunc
	screenshotDir string
	now           func() time.Time
}

type ChromedpOptions struct {
	ParentContext context.Context
	AllocatorOpts []chromedp.ExecAllocatorOption
	ScreenshotDir string
}

func NewChromedpRuntime(ctx context.Context, opts ChromedpOptions) (*ChromedpRuntime, error) {
	parent := opts.ParentContext
	if parent == nil {
		parent = ctx
	}
	if parent == nil {
		parent = context.Background()
	}
	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(parent, opts.AllocatorOpts...)
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
	cancel := func() {
		browserCancel()
		allocatorCancel()
	}
	return &ChromedpRuntime{
		ctx:           browserCtx,
		cancel:        cancel,
		screenshotDir: opts.ScreenshotDir,
		now:           time.Now,
	}, nil
}

func (r *ChromedpRuntime) Close() {
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *ChromedpRuntime) Navigate(ctx context.Context, rawURL string) (Observation, error) {
	if rawURL == "" {
		return Observation{}, errors.New("browser navigation URL is required")
	}
	if err := r.run(ctx, chromedp.Navigate(rawURL)); err != nil {
		return Observation{}, err
	}
	return r.Observe(ctx)
}

func (r *ChromedpRuntime) Observe(ctx context.Context) (Observation, error) {
	var title string
	var location string
	var text string
	var dom string
	err := r.run(ctx,
		chromedp.Title(&title),
		chromedp.Location(&location),
		chromedp.Text("body", &text, chromedp.ByQuery),
		chromedp.OuterHTML("html", &dom, chromedp.ByQuery),
	)
	if err != nil {
		return Observation{}, err
	}
	return Observation{
		URL:         location,
		Title:       title,
		VisibleText: summarizeText(text, 4096),
		DOMSummary:  summarizeText(dom, 4096),
	}, nil
}

func (r *ChromedpRuntime) Locate(ctx context.Context, target Target) (LocatedElement, error) {
	selector := selectorForTarget(target)
	if selector == "" {
		return LocatedElement{}, ErrElementNotFound
	}
	var text string
	if err := r.run(ctx, chromedp.Text(selector, &text, queryOptionForTarget(target))); err != nil {
		return LocatedElement{}, err
	}
	return LocatedElement{
		Selector: selector,
		Role:     target.Role,
		Text:     strings.TrimSpace(text),
		Target:   target,
	}, nil
}

func (r *ChromedpRuntime) Click(ctx context.Context, target Target) (Observation, error) {
	if hasCoordinates(target) && !hasSemanticTarget(target) {
		if err := r.run(ctx, chromedp.MouseClickXY(float64(target.X), float64(target.Y))); err != nil {
			return Observation{}, err
		}
		return r.Observe(ctx)
	}
	selector := selectorForTarget(target)
	if selector == "" {
		return Observation{}, ErrElementNotFound
	}
	if err := r.run(ctx, chromedp.Click(selector, queryOptionForTarget(target))); err != nil {
		return Observation{}, err
	}
	return r.Observe(ctx)
}

func (r *ChromedpRuntime) MoveMouse(ctx context.Context, x int, y int) (Observation, error) {
	if err := r.run(ctx, chromedp.MouseEvent(input.MouseMoved, float64(x), float64(y))); err != nil {
		return Observation{}, err
	}
	return r.Observe(ctx)
}

func (r *ChromedpRuntime) MouseWheel(ctx context.Context, deltaX int, deltaY int) (Observation, error) {
	if err := r.run(ctx, chromedp.ActionFunc(func(cdpCtx context.Context) error {
		return input.DispatchMouseEvent(input.MouseWheel, 0, 0).WithDeltaX(float64(deltaX)).WithDeltaY(float64(deltaY)).Do(cdpCtx)
	})); err != nil {
		return Observation{}, err
	}
	return r.Observe(ctx)
}

func (r *ChromedpRuntime) TypeText(ctx context.Context, target Target, text string) (Observation, error) {
	selector := selectorForTarget(target)
	if selector != "" {
		if err := r.run(ctx, chromedp.SendKeys(selector, text, queryOptionForTarget(target))); err != nil {
			return Observation{}, err
		}
		return r.Observe(ctx)
	}
	if hasCoordinates(target) {
		if err := r.run(ctx, chromedp.MouseClickXY(float64(target.X), float64(target.Y)), chromedp.KeyEvent(text)); err != nil {
			return Observation{}, err
		}
		return r.Observe(ctx)
	}
	return Observation{}, ErrElementNotFound
}

func (r *ChromedpRuntime) KeyboardShortcut(ctx context.Context, keys []string) (Observation, error) {
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if err := r.run(ctx, chromedp.KeyEvent(key)); err != nil {
			return Observation{}, err
		}
	}
	return r.Observe(ctx)
}

func (r *ChromedpRuntime) AccessibilityTree(ctx context.Context) (Observation, error) {
	var summary string
	err := r.run(ctx, chromedp.ActionFunc(func(cdpCtx context.Context) error {
		nodes, err := accessibility.GetFullAXTree().WithDepth(4).Do(cdpCtx)
		if err != nil {
			return err
		}
		summary = SummarizeA11yNodes(nodes, 4096)
		return nil
	}))
	if err != nil {
		return Observation{}, err
	}
	obs, err := r.Observe(ctx)
	if err != nil {
		return Observation{}, err
	}
	obs.A11ySummary = summary
	return obs, nil
}

func (r *ChromedpRuntime) Screenshot(ctx context.Context) (Observation, error) {
	if r.screenshotDir == "" {
		return Observation{}, errors.New("browser screenshot directory is required")
	}
	var content []byte
	if err := r.run(ctx, chromedp.CaptureScreenshot(&content)); err != nil {
		return Observation{}, err
	}
	if err := os.MkdirAll(r.screenshotDir, 0o700); err != nil {
		return Observation{}, err
	}
	name := fmt.Sprintf("browser-%d.png", r.clock().UnixNano())
	path := filepath.Join(r.screenshotDir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return Observation{}, err
	}
	obs, err := r.Observe(ctx)
	if err != nil {
		return Observation{}, err
	}
	obs.ScreenshotID = path
	return obs, nil
}

func (r *ChromedpRuntime) run(ctx context.Context, actions ...chromedp.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- chromedp.Run(r.ctx, actions...)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (r *ChromedpRuntime) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func queryOptionForTarget(target Target) chromedp.QueryOption {
	if target.Selector != "" {
		return chromedp.ByQuery
	}
	return chromedp.BySearch
}
