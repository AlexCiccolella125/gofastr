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

// keys and entries take a prefix and enumerate only what lies under it,
// through the engine's key range rather than a scan: a layer that
// groups records under a common prefix can list one group without
// reading its neighbours, and an entry's size is the stored UTF-8
// length of its JSON text.
func TestLocalPrefixEnumeration(t *testing.T) {
	srv := startLocalServer(t, "")
	ctx := chromedptest.Context(t, chromedptest.Timeout(90*time.Second))
	openLocal(t, ctx, srv.URL+"/")

	if err := chromedp.Run(ctx, chromedp.Evaluate(`Promise.all([
        window.__gofastr.local.set('local.site.drafts:b', { title: 'second' }),
        window.__gofastr.local.set('local.site.drafts:a', { title: 'first' }),
        window.__gofastr.local.set('local.site.draftsx:z', { title: 'neighbour' }),
        window.__gofastr.local.set('local.site.prefs:theme', 'dark'),
        window.__gofastr.local.set('unrelated', 1),
    ])`, nil, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}

	var keys []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.keys('local.site.drafts:')`, &keys, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "local.site.drafts:a" || keys[1] != "local.site.drafts:b" {
		t.Fatalf("keys(prefix) = %v, want exactly the two drafts, sorted — a sibling prefix (draftsx) or another group leaked in", keys)
	}
	var all []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.keys()`, &all, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("keys() = %v, want all five — the no-prefix form must still name the whole namespace", all)
	}

	var entries []map[string]any
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.entries('local.site.drafts:')`, &entries, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries(prefix) = %v, want two", entries)
	}
	first, _ := entries[0]["value"].(map[string]any)
	if entries[0]["key"] != "local.site.drafts:a" || first["title"] != "first" {
		t.Fatalf("entries(prefix)[0] = %v, want the a record with its value", entries[0])
	}
	// {"title":"first"} is 17 bytes of JSON.
	if size, _ := entries[0]["size"].(float64); int(size) != 17 {
		t.Fatalf("entries(prefix)[0].size = %v, want 17 (the UTF-8 length of the stored JSON text)", entries[0]["size"])
	}
}

// The fallback engine enumerates by prefix too, so a layer above sees
// one contract whichever engine answered.
func TestLocalPrefixEnumerationOnTheFallback(t *testing.T) {
	srv := startLocalServer(t, noIDB)
	ctx := chromedptest.Context(t, chromedptest.Timeout(90*time.Second))
	openLocal(t, ctx, srv.URL+"/")

	if err := chromedp.Run(ctx, chromedp.Evaluate(`Promise.all([
        window.__gofastr.local.set('g:1', 'é'),
        window.__gofastr.local.set('g:2', 'b'),
        window.__gofastr.local.set('h:1', 'c'),
    ])`, nil, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.local.entries('g:')`, &entries, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0]["key"] != "g:1" || entries[1]["key"] != "g:2" {
		t.Fatalf("fallback entries('g:') = %v, want g:1 and g:2", entries)
	}
	// "é" is 4 bytes of JSON text: two quotes and a two-byte rune. The
	// size is bytes, not code units.
	if size, _ := entries[0]["size"].(float64); int(size) != 4 {
		t.Fatalf("fallback entries('g:')[0].size = %v, want 4", entries[0]["size"])
	}
}

// watch hears another tab's write of any key under a prefix, and never
// this tab's own: one watcher covers a whole group of records.
func TestLocalWatchCrossesTabsByPrefix(t *testing.T) {
	srv := startLocalServer(t, "")
	tabA := chromedptest.Context(t, chromedptest.Timeout(90*time.Second))
	openLocal(t, tabA, srv.URL+"/")
	if err := chromedp.Run(tabA, chromedp.Evaluate(`(() => {
        window.__heard = [];
        window.__gofastr.local.watch('grp:', (k) => window.__heard.push(k));
        window.__gofastr.local.set('grp:mine', 1);
    })()`, nil)); err != nil {
		t.Fatal(err)
	}

	tabB, cancelB := chromedp.NewContext(tabA)
	t.Cleanup(cancelB)
	openLocal(t, tabB, srv.URL+"/")
	if err := chromedp.Run(tabB, chromedp.Evaluate(`Promise.all([
        window.__gofastr.local.set('other:x', 1),
        window.__gofastr.local.set('grp:theirs', 2),
    ])`, nil, awaitLocalPromise)); err != nil {
		t.Fatal(err)
	}

	if !localPollTrue(tabA, `Promise.resolve(window.__heard.indexOf('grp:theirs') >= 0)`) {
		var heard []string
		_ = chromedp.Run(tabA, chromedp.Evaluate(`window.__heard`, &heard))
		t.Fatalf("tab A heard %v — the sibling tab's write under the prefix never arrived", heard)
	}
	var heard []string
	if err := chromedp.Run(tabA, chromedp.Evaluate(`window.__heard`, &heard)); err != nil {
		t.Fatal(err)
	}
	for _, k := range heard {
		if k == "grp:mine" {
			t.Fatal("tab A heard its own write back")
		}
		if k == "other:x" {
			t.Fatal("a watcher heard a key outside its prefix")
		}
	}
}
