package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DonaldMurillo/gofastr/internal/chromedptest"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Browser coverage for src/local.js, the browser-store primitive: the
// engine is IndexedDB, the fallback is localStorage for tiny values
// only, every key lives under one namespace, and a second tab of the
// same origin hears a write.
//
// The module has no marker; these tests load it the way an application
// does, with __gofastr.loadModule('local').

func awaitLocalPromise(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

// startLocalServer serves runtime.js and the runtime modules, plus one
// page. head is injected into <head> before runtime.js runs, which is
// where a test disables IndexedDB to reach the fallback.
func startLocalServer(t *testing.T, head string) *httptest.Server {
	t.Helper()
	js, err := RuntimeJS()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Module("local"); !ok {
		t.Fatal("src/local.js is not embedded")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/__gofastr/runtime.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(js))
	})
	mux.HandleFunc("/__gofastr/runtime/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/__gofastr/runtime/"), ".js")
		w.Header().Set("Content-Type", "application/javascript")
		if src, ok := Module(name); ok {
			_, _ = w.Write([]byte(src))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><html><head><title>local</title>%s</head><body>
  <main role="main"><span id="ready">ready</span></main>
  <script src="/__gofastr/runtime.js"></script>
</body></html>`, head)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// openLocal navigates and loads the primitive, failing the test if it
// never registers.
func openLocal(t *testing.T, ctx context.Context, url string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
		chromedp.Evaluate(`window.__gofastr.loadModule('local')`, nil, awaitLocalPromise),
	); err != nil {
		t.Fatalf("loading the local module: %v", err)
	}
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`!!window.__gofastr.local`, &ok)); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the local module ran without publishing window.__gofastr.local")
	}
}

func localPollTrue(ctx context.Context, js string) bool {
	for range 60 {
		var v bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &v, awaitLocalPromise)); err == nil && v {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// The engine is IndexedDB: a value round-trips with its type intact, it
// is enumerable by its application key, remove clears it, and none of
// it lands in localStorage.
func TestLocalRoundTripsThroughIndexedDB(t *testing.T) {
	srv := startLocalServer(t, "")
	ctx := chromedptest.Context(t, chromedptest.Timeout(90*time.Second))
	openLocal(t, ctx, srv.URL+"/")

	var avail map[string]any
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.available()`, &avail, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if idb, _ := avail["idb"].(bool); !idb {
		t.Fatalf("available() = %v, want idb true — this test would otherwise measure the fallback", avail)
	}

	var res map[string]any
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.__gofastr.local.set('teams', { list: ['a', 'b'], n: 2 })`, &res, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("set() = %v, want ok", res)
	}
	var round string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.__gofastr.local.get('teams').then((v) => JSON.stringify(v))`, &round, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if round != `{"list":["a","b"],"n":2}` {
		t.Fatalf("get() = %s — the value did not round-trip with its type", round)
	}
	var keys []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.keys()`, &keys, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "teams" {
		t.Fatalf("keys() = %v, want [teams] — the namespace must be stripped, not leaked", keys)
	}
	// Nothing touched Web storage: IndexedDB is the engine, not a cache
	// in front of localStorage.
	var lsCount int
	if err := chromedp.Run(ctx, chromedp.Evaluate(`Object.keys(localStorage).length`, &lsCount)); err != nil {
		t.Fatal(err)
	}
	if lsCount != 0 {
		t.Fatalf("%d localStorage keys written while IndexedDB was available", lsCount)
	}

	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.remove('teams')`, nil, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.__gofastr.local.get('teams').then((v) => v === undefined ? 'gone' : 'still-there')`, &after, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if after != "gone" {
		t.Fatalf("after remove(), get() says %q", after)
	}
}

// A read of a key nobody wrote is undefined, not a throw and not a
// stale neighbour: the best-effort contract's quiet half.
func TestLocalMissingKeyIsUndefined(t *testing.T) {
	srv := startLocalServer(t, "")
	ctx := chromedptest.Context(t, chromedptest.Timeout(90*time.Second))
	openLocal(t, ctx, srv.URL+"/")

	var got string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.__gofastr.local.get('never-written').then((v) => v === undefined ? 'undefined' : JSON.stringify(v))`, &got, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if got != "undefined" {
		t.Fatalf("get() on an absent key = %s", got)
	}
}

// noIDB blanks window.indexedDB before runtime.js runs, which is the
// only honest way to reach the fallback path: the module caches the
// open attempt, so the API has to be gone before the first call.
const noIDB = `<script>Object.defineProperty(window, 'indexedDB', { value: undefined, configurable: true });</script>`

// Without IndexedDB the primitive falls back to localStorage, under the
// same namespace — and only for tiny values. A larger one is refused
// with a reason rather than silently dropped or half-written.
func TestLocalFallsBackToLocalStorageForTinyValuesOnly(t *testing.T) {
	srv := startLocalServer(t, noIDB)
	ctx := chromedptest.Context(t, chromedptest.Timeout(90*time.Second))
	openLocal(t, ctx, srv.URL+"/")

	var avail map[string]any
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.available()`, &avail, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if idb, _ := avail["idb"].(bool); idb {
		t.Fatal("IndexedDB was still reachable — the fallback path is untested")
	}
	if ls, _ := avail["ls"].(bool); !ls {
		t.Fatalf("available() = %v, want ls true", avail)
	}

	var res map[string]any
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.set('pref', 'dark')`, &res, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("a tiny value was refused by the fallback: %v", res)
	}
	var stored string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`localStorage.getItem('gofastr.state.' + encodeURIComponent('pref')) || ''`, &stored)); err != nil {
		t.Fatal(err)
	}
	if stored != `"dark"` {
		t.Fatalf("fallback stored %q under gofastr.state.pref, want the JSON text — the namespace or the engine is wrong", stored)
	}
	var round string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.__gofastr.local.get('pref').then((v) => String(v))`, &round, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if round != "dark" {
		t.Fatalf("fallback get() = %q", round)
	}
	var keys []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.keys()`, &keys, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "pref" {
		t.Fatalf("fallback keys() = %v, want [pref]", keys)
	}

	// Over the fallback's tiny-value cap: refused, with a reason, and
	// nothing written.
	var big map[string]any
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.__gofastr.local.set('bulk', 'x'.repeat(20000))`, &big, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := big["ok"].(bool); ok {
		t.Fatal("the fallback accepted a 20 KB value — localStorage is the origin's whole budget")
	}
	if reason, _ := big["reason"].(string); reason != "size" {
		t.Fatalf("set() refusal reason = %q, want \"size\"", reason)
	}
	var bulk string
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`localStorage.getItem('gofastr.state.' + encodeURIComponent('bulk')) || 'absent'`, &bulk)); err != nil {
		t.Fatal(err)
	}
	if bulk != "absent" {
		t.Fatal("a refused write still reached localStorage")
	}
}

// An application key can never name storage outside the namespace, and
// a key that would collide with one if it were not encoded does not.
func TestLocalKeysStayInsideTheNamespace(t *testing.T) {
	srv := startLocalServer(t, noIDB)
	ctx := chromedptest.Context(t, chromedptest.Timeout(90*time.Second))
	openLocal(t, ctx, srv.URL+"/")

	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`localStorage.setItem('gofastr.colorScheme', 'light')`, nil),
		chromedp.Evaluate(`window.__gofastr.local.set('../colorScheme', 'dark')`, nil, awaitLocalPromise),
		chromedp.Evaluate(`window.__gofastr.local.set('gofastr.colorScheme', 'dark')`, nil, awaitLocalPromise),
	); err != nil {
		t.Fatal(err)
	}
	var scheme string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`localStorage.getItem('gofastr.colorScheme') || ''`, &scheme)); err != nil {
		t.Fatal(err)
	}
	if scheme != "light" {
		t.Fatalf("an application key reached another feature's storage: gofastr.colorScheme = %q", scheme)
	}
	var outside int
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`Object.keys(localStorage).filter((k) => k.indexOf('gofastr.state.') !== 0 && k !== 'gofastr.colorScheme').length`, &outside)); err != nil {
		t.Fatal(err)
	}
	if outside != 0 {
		t.Fatalf("%d keys written outside the namespace", outside)
	}
}

// Two real tabs of the same origin: a subscriber in one hears the
// other's write, and never its own.
func TestLocalSubscribeCrossesTabs(t *testing.T) {
	srv := startLocalServer(t, "")
	tabA := chromedptest.Context(t, chromedptest.Timeout(90*time.Second))
	openLocal(t, tabA, srv.URL+"/")
	if err := chromedp.Run(tabA, chromedp.Evaluate(`(() => {
        window.__heard = [];
        window.__gofastr.local.subscribe('shared', (v) => window.__heard.push(v));
        window.__gofastr.local.set('shared', 'written-here');
    })()`, nil)); err != nil {
		t.Fatal(err)
	}

	tabB, cancelB := chromedp.NewContext(tabA)
	t.Cleanup(cancelB)
	openLocal(t, tabB, srv.URL+"/")
	if err := chromedp.Run(tabB, chromedp.Evaluate(
		`window.__gofastr.local.set('shared', 'written-next-door')`, nil, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}

	if !localPollTrue(tabA, `Promise.resolve(window.__heard.indexOf('written-next-door') >= 0)`) {
		var heard []string
		_ = chromedp.Run(tabA, chromedp.Evaluate(`window.__heard`, &heard))
		t.Fatalf("tab A heard %v — the sibling tab's write never arrived", heard)
	}
	var heard []string
	if err := chromedp.Run(tabA, chromedp.Evaluate(`window.__heard`, &heard)); err != nil {
		t.Fatal(err)
	}
	for _, v := range heard {
		if v == "written-here" {
			t.Fatal("tab A heard its own write back — a subscriber is a cross-tab channel, not a change feed")
		}
	}
}
