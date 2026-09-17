package main

// The headline claim, proven in a real browser: the team survives
// quitting the browser. One Chrome profile directory, two Chrome
// launches. The first builds a team and checks it; Chrome is closed
// gracefully; the second launch opens the same page and the roster
// and the verdict are painted from the profile's IndexedDB. A third
// launch on a fresh profile sees nothing, so what persisted was the
// profile, not the server.
//
// Skips in -short mode. The server is one httptest server for all
// three launches, so the origin (and with it the IndexedDB database)
// is the same each time.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// launch starts a headless Chrome on the given profile directory. The
// returned close shuts it down gracefully (chromedp.Cancel waits for
// Browser.Close), which is what lets IndexedDB flush to the profile.
func launch(t *testing.T, profileDir string) (ctx context.Context, close func()) {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.UserDataDir(profileDir),
		chromedp.WSURLReadTimeout(90*time.Second),
		chromedp.WindowSize(1280, 800),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browser, browserCancel := chromedp.NewContext(allocCtx)
	if err := chromedp.Run(browser); err != nil {
		browserCancel()
		allocCancel()
		t.Fatalf("browser failed to start: %v", err)
	}
	ctx, tcancel := context.WithTimeout(browser, 120*time.Second)
	closed := false
	close = func() {
		if closed {
			return
		}
		closed = true
		tcancel()
		if err := chromedp.Cancel(browser); err != nil {
			t.Logf("browser close: %v", err)
		}
		allocCancel()
	}
	t.Cleanup(close)
	return ctx, close
}

// pollTrue evaluates js (a promise of a boolean) until it resolves
// true or the budget runs out.
func pollTrue(ctx context.Context, js string) bool {
	for range 100 {
		var v bool
		err := chromedp.Run(ctx, chromedp.Evaluate(js, &v, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}))
		if err == nil && v {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func addMember(t *testing.T, ctx context.Context, name, role string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.SetValue(`#member-name`, name, chromedp.ByID),
		chromedp.SetValue(`#member-role`, role, chromedp.ByID),
		chromedp.Click(`#add-member`, chromedp.ByID),
	); err != nil {
		t.Fatal(err)
	}
}

const rosterHas = `Array.from(document.querySelectorAll('#roster li')).map(li => li.textContent)`

func TestBrowser_TeamSurvivesQuittingTheBrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("browser e2e: -short")
	}
	srv := httptest.NewServer(buildApp().Router())
	t.Cleanup(srv.Close)
	profile := t.TempDir()

	// Session one: build a team, check it, and quit.
	ctx, closeBrowser := launch(t, profile)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible(`#add-member`, chromedp.ByID),
	); err != nil {
		t.Fatal(err)
	}
	addMember(t, ctx, "Garchomp", "attacker")
	if !pollTrue(ctx, `Promise.resolve(document.querySelectorAll('#roster li').length === 1)`) {
		t.Fatal("the first member never reached the roster")
	}
	addMember(t, ctx, "Corviknight", "defender")
	if !pollTrue(ctx, `Promise.resolve(document.querySelectorAll('#roster li').length === 2 && document.getElementById('roster-count').textContent === '2 of 6 slots filled.')`) {
		t.Fatal("the second member never reached the roster")
	}
	if err := chromedp.Run(ctx, chromedp.Click(`#check-form button[type=submit]`, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if !pollTrue(ctx, `Promise.resolve(document.getElementById('check-result').textContent === 'Team of 2 is ready.')`) {
		var got string
		_ = chromedp.Run(ctx, chromedp.Text(`#check-result`, &got, chromedp.ByID))
		t.Fatalf("check result = %q: the handler did not read the team", got)
	}
	// The response wrote the verdict back; the page painted it from
	// the store, which is the only place it lives.
	if !pollTrue(ctx, `Promise.resolve(document.getElementById('last-verdict').textContent.indexOf('Team of 2 is ready.') >= 0)`) {
		t.Fatal("the verdict the response pushed never showed up")
	}
	closeBrowser()

	// Session two: same profile, same origin. Nothing was typed.
	ctx, closeBrowser = launch(t, profile)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible(`#add-member`, chromedp.ByID),
	); err != nil {
		t.Fatal(err)
	}
	var roster []string
	if !pollTrue(ctx, `Promise.resolve(document.querySelectorAll('#roster li').length === 2)`) {
		_ = chromedp.Run(ctx, chromedp.Evaluate(rosterHas, &roster))
		t.Fatalf("after quitting and relaunching the browser the roster is %v: the team did not survive", roster)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(rosterHas, &roster)); err != nil {
		t.Fatal(err)
	}
	if len(roster) != 2 || roster[0] != "Garchomp — attacker" || roster[1] != "Corviknight — defender" {
		t.Fatalf("roster after relaunch = %v", roster)
	}
	if !pollTrue(ctx, `Promise.resolve(document.getElementById('last-verdict').textContent.indexOf('Team of 2 is ready.') >= 0)`) {
		t.Fatal("the verdict did not survive the relaunch")
	}
	// The check result signal is not persisted, on purpose: it is the
	// response's paint, and the server rendered the default again.
	var result string
	if err := chromedp.Run(ctx, chromedp.Text(`#check-result`, &result, chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	if result != "Not checked yet." {
		t.Fatalf("check result after relaunch = %q, want the server default", result)
	}
	closeBrowser()

	// Session three: a fresh profile sees nothing. The team was in the
	// browser, never on the server.
	ctx, _ = launch(t, t.TempDir())
	if err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible(`#add-member`, chromedp.ByID),
	); err != nil {
		t.Fatal(err)
	}
	if !pollTrue(ctx, `window.__gofastr.loadModule('local-store').then(() => window.__gofastr.localStore('team-builder').collection('teams').get('current')).then(t => t === null || t === undefined)`) {
		t.Fatal("a fresh profile found a team: state leaked through the server")
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(rosterHas, &roster)); err != nil {
		t.Fatal(err)
	}
	if len(roster) != 0 {
		t.Fatalf("fresh profile roster = %v, want empty", roster)
	}
}
