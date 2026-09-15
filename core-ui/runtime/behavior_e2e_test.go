package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DonaldMurillo/gofastr/core-ui/registry"
	"github.com/DonaldMurillo/gofastr/internal/chromedptest"
	cdruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Browser coverage for the behaviour seam (docs/spec-behavior-registry.md,
// "What must be tested" → browser, in core-ui/runtime). A registered
// behaviour is a module from the host down: the kernel learns its markers
// from the inline #gofastr-behaviors block or the
// window.__gofastr_behaviors global, scans for them exactly as it scans
// its own table, loads the module once, and hands it swapped-in DOM.

// probeBehaviorJS is an IIFE keeping the module contract (spec section
// "The module contract"): binds only its own marker, sets
// loadedModules[name] when attached, registers an idempotent scanner on
// _moduleScanners[name] so the kernel can hand it inserted DOM and the
// post-navigation document.
const probeBehaviorJS = `(function () {
  'use strict';
  var NAME = 'probe-beh';
  var NS = window.__gofastr = window.__gofastr || {};
  function wire(el) {
    if (el.getAttribute('data-probe-attached')) return;
    el.setAttribute('data-probe-attached', '1');
  }
  function scan(root) {
    var scope = root && root.querySelectorAll ? root : document;
    if (scope.matches && scope.matches('[data-probe]')) wire(scope);
    var nodes = scope.querySelectorAll('[data-probe]');
    for (var i = 0; i < nodes.length; i++) wire(nodes[i]);
  }
  scan(document);
  NS.loadedModules = NS.loadedModules || {};
  NS.loadedModules[NAME] = true;
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
})();`

// registerProbe isolates the registry and registers probe-beh with the
// given extra options (Markers("[data-probe]") is always applied).
func registerProbe(t *testing.T, opts ...registry.BehaviorOption) {
	t.Helper()
	registry.IsolateForTest(t)
	all := append([]registry.BehaviorOption{registry.Markers("[data-probe]")}, opts...)
	registry.RegisterBehavior("probe-beh", probeBehaviorJS, all...)
}

// probeServer serves runtime.js, the probe module (500 when fail is set)
// and one page built from head and body. It counts module fetches and
// records the last fetched URL.
type probeServer struct {
	srv     *httptest.Server
	hits    atomic.Int32
	lastURL atomic.Value // string
}

func startProbeServer(t *testing.T, head, body string, fail bool) *probeServer {
	t.Helper()
	js, err := RuntimeJS()
	if err != nil {
		t.Fatal(err)
	}
	mod, ok := Module("probe-beh")
	if !ok {
		t.Fatal("probe-beh not served by runtime.Module after registration")
	}
	p := &probeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/__gofastr/runtime.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte(js))
	})
	mux.HandleFunc("/__gofastr/runtime/probe-beh.js", func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		p.lastURL.Store(r.URL.String())
		w.Header().Set("Content-Type", "application/javascript")
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(mod))
	})
	// Other embedded modules the kernel decides to load (activelink and
	// friends) must get JavaScript, not the page HTML: an HTML body in a
	// <script> is an uncaught SyntaxError that has nothing to do with the
	// behaviour under test.
	mux.HandleFunc("/__gofastr/runtime/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/__gofastr/runtime/"), ".js")
		w.Header().Set("Content-Type", "application/javascript")
		if src, ok := Module(name); ok {
			w.Write([]byte(src))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><html><head><title>probe</title>%s</head><body>
  <main role="main"><span id="ready">ready</span>%s</main>
  <script src="/__gofastr/runtime.js"></script>
</body></html>`, head, body)
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// inlineBehaviorsBlock builds the #gofastr-behaviors block from
// BehaviorsJSON, the shape widget.BehaviorsManifestScript emits (built
// here directly: importing core-ui/widget from core-ui/runtime would be
// an import cycle).
func inlineBehaviorsBlock(t *testing.T) string {
	t.Helper()
	buf := BehaviorsJSON()
	if buf == nil {
		t.Fatal("BehaviorsJSON returned nil for a registered behaviour")
	}
	return `<script type="application/json" id="gofastr-behaviors">` + string(buf) + `</script>`
}

// pollTrue evaluates js (a boolean expression) until it is true or the
// bounded budget runs out. Polls instead of sleeping a fixed settle.
func pollTrue(ctx context.Context, js string) bool {
	for range 40 {
		var v bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &v)); err == nil && v {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

const attachedExpr = `!!(document.querySelector('[data-probe]') && document.querySelector('[data-probe]').getAttribute('data-probe-attached'))`

// A page whose DOM carries the marker fetches the registered module
// exactly once and the module attaches (writes its probe attribute).
func TestBehaviorLoadsOnMarker(t *testing.T) {
	registerProbe(t)
	p := startProbeServer(t, inlineBehaviorsBlock(t), `<p data-probe>probe target</p>`, false)
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))

	if err := chromedp.Run(ctx,
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, attachedExpr) {
		t.Fatal("marker element never got data-probe-attached after module load")
	}
	if n := p.hits.Load(); n != 1 {
		t.Fatalf("module fetched %d times, want exactly 1", n)
	}
}

// A page without the marker never fetches the module.
func TestBehaviorNoMarkerNoFetch(t *testing.T) {
	registerProbe(t)
	p := startProbeServer(t, inlineBehaviorsBlock(t), ``, false)
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))

	if err := chromedp.Run(ctx,
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
		chromedp.Sleep(700*time.Millisecond),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if n := p.hits.Load(); n != 0 {
		t.Fatalf("module fetched %d times without any marker, want 0", n)
	}
}

// A marker inserted after boot (island swap, widget mount) loads the
// module through the insertion scan and attaches.
func TestBehaviorLoadsOnInsertedMarker(t *testing.T) {
	registerProbe(t)
	p := startProbeServer(t, inlineBehaviorsBlock(t), ``, false)
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))

	if err := chromedp.Run(ctx,
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
		chromedp.Evaluate(`(() => {
            const el = document.createElement('p');
            el.setAttribute('data-probe', '1');
            el.id = 'inserted';
            el.textContent = 'late marker';
            document.body.appendChild(el);
        })()`, nil),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, attachedExpr) {
		t.Fatal("inserted marker never attached — MutationObserver scan missed the registered behaviour")
	}
	if n := p.hits.Load(); n != 1 {
		t.Fatalf("module fetched %d times for inserted marker, want 1", n)
	}
}

// After a client navigation the module's registered scanner runs over
// the new document: the module is already loaded (no second fetch) and
// fresh markers in the swapped content attach.
func TestBehaviorScannerRunsAfterNavigate(t *testing.T) {
	registerProbe(t)
	p := startProbeServer(t, inlineBehaviorsBlock(t), `<p data-probe>first page marker</p>`, false)
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))

	if err := chromedp.Run(ctx,
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, attachedExpr) {
		t.Fatal("module never attached on the first page")
	}
	// Swap in a fresh document body (what a SPA nav does to <main>),
	// then fire the navigate event the runtime dispatches after a swap.
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
            const main = document.querySelector('main');
            main.innerHTML = '<span id="ready">ready</span><p data-probe>new document marker</p>';
            window.dispatchEvent(new CustomEvent('gofastr:navigate', { detail: { path: '/next' } }));
        })()`, nil),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, attachedExpr) {
		t.Fatal("scanner never ran over the post-navigation document")
	}
	if n := p.hits.Load(); n != 1 {
		t.Fatalf("module fetched %d times across a client navigation, want 1 (scanner reuse, no refetch)", n)
	}
}

// LoadIdle defers the load through requestIdleCallback and the module
// still attaches.
func TestBehaviorLoadIdleAttaches(t *testing.T) {
	registerProbe(t, registry.LoadIdle())
	p := startProbeServer(t, inlineBehaviorsBlock(t), `<p data-probe>idle target</p>`, false)
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))

	if err := chromedp.Run(ctx,
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	// rIC (or the setTimeout fallback) fires within the poll budget.
	if !pollTrue(ctx, attachedExpr) {
		t.Fatal("idle behaviour never attached after the idle callback ran")
	}
	if n := p.hits.Load(); n != 1 {
		t.Fatalf("module fetched %d times, want 1", n)
	}
}

// data-fui-prefetch="<name>" on hover fetches the registered module
// before any click, the warm-the-cache path.
func TestBehaviorHoverPrefetch(t *testing.T) {
	registerProbe(t)
	p := startProbeServer(t, inlineBehaviorsBlock(t), ``, false)
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))

	if err := chromedp.Run(ctx,
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
		chromedp.Evaluate(`(() => {
            const btn = document.createElement('button');
            btn.setAttribute('data-fui-prefetch', 'probe-beh');
            btn.textContent = 'prefetch probe';
            document.body.appendChild(btn);
            btn.dispatchEvent(new PointerEvent('pointerover', { bubbles: true }));
        })()`, nil),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `!!window.__gofastr.loadedModules['probe-beh']`) {
		t.Fatal("hover on data-fui-prefetch never loaded the registered module")
	}
	if n := p.hits.Load(); n != 1 {
		t.Fatalf("module fetched %d times on hover prefetch, want 1", n)
	}
}

// A failing fetch (500 on the module URL) leaves the page usable: the
// marker element stays, loadedModules stays falsy, and no uncaught
// error escapes. The failed load surfaces on the console through the
// browser's network error entry, never as an unhandled rejection.
func TestBehaviorFailedFetchLeavesPageUsable(t *testing.T) {
	registerProbe(t)
	p := startProbeServer(t, inlineBehaviorsBlock(t), `<p data-probe>doomed</p>`, true)

	var exceptions atomic.Int32
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))
	chromedp.ListenTarget(ctx, func(ev any) {
		if _, ok := ev.(*cdruntime.EventExceptionThrown); ok {
			exceptions.Add(1)
		}
	})

	var stillThere, loaded, alive bool
	if err := chromedp.Run(ctx,
		cdruntime.Enable(),
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
		chromedp.Sleep(800*time.Millisecond),
		chromedp.Evaluate(`!!document.querySelector('[data-probe]')`, &stillThere),
		chromedp.Evaluate(`!!(window.__gofastr.loadedModules && window.__gofastr.loadedModules['probe-beh'])`, &loaded),
		chromedp.Evaluate(`1 + 1 === 2`, &alive),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if p.hits.Load() == 0 {
		t.Fatal("module was never requested — failure path not exercised")
	}
	if !stillThere {
		t.Error("marker element disappeared after a failed module fetch")
	}
	if loaded {
		t.Error("loadedModules['probe-beh'] is truthy after a failed fetch")
	}
	if !alive {
		t.Error("page stopped evaluating scripts after a failed module fetch")
	}
	if n := exceptions.Load(); n != 0 {
		t.Fatalf("%d uncaught exceptions after a failed module fetch", n)
	}
}

// The global path: window.__gofastr_behaviors assigned by a classic
// script BEFORE runtime.js is honoured when no inline block exists.
func TestBehaviorGlobalManifestPath(t *testing.T) {
	registerProbe(t)
	head := `<script>window.__gofastr_behaviors = {"probe-beh":{"s":["[data-probe]"]}};</script>`
	p := startProbeServer(t, head, `<p data-probe>global path</p>`, false)
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))

	if err := chromedp.Run(ctx,
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, attachedExpr) {
		t.Fatal("behaviour declared only through window.__gofastr_behaviors never attached")
	}
	if n := p.hits.Load(); n != 1 {
		t.Fatalf("module fetched %d times, want 1", n)
	}
}

// The module is fetched with the ?v=<hash> the module manifest carries:
// the fetched URL must carry the hash runtime.ModuleHash computed.
func TestBehaviorFetchCarriesManifestHash(t *testing.T) {
	registerProbe(t)
	hash := ModuleHash("probe-beh")
	if hash == "" {
		t.Fatal("ModuleHash returned empty for a registered behaviour")
	}
	head := inlineBehaviorsBlock(t) +
		`<script type="application/json" id="gofastr-runtime-modules">{"probe-beh":"` + hash + `"}</script>`
	p := startProbeServer(t, head, `<p data-probe>versioned</p>`, false)
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))

	if err := chromedp.Run(ctx,
		chromedp.Navigate(p.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, attachedExpr) {
		t.Fatal("module never attached; hash-bearing manifest test setup broken")
	}
	got, _ := p.lastURL.Load().(string)
	want := "/__gofastr/runtime/probe-beh.js?v=" + hash
	if got != want {
		t.Fatalf("fetched module URL = %q, want %q", got, want)
	}
}
