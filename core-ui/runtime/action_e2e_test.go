package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DonaldMurillo/gofastr/internal/chromedptest"
	"github.com/chromedp/chromedp"
)

// way the adapters drive it. The primitive has no marker, so the page
// arms it the way any owner does: loadModule('action') and
// window.__gofastr.action.bind(el, spec).

// actionSrv serves the runtime, every module by name, and two mutation
// endpoints: /ok settles 204 and /fail settles 422. It counts endpoint
// hits so the tests can prove one request per click.
type actionSrv struct {
	srv  *httptest.Server
	ok   atomic.Int32
	fail atomic.Int32
}

// actionPage is the arm script every test body appends its buttons
// before. The bind happens after loadModule resolves, which is the
// same ordering the loader guarantees a module that Requires("action").
const actionArm = `<script>
(function () {
  window.__fetches = [];
  var realFetch = window.fetch.bind(window);
  window.fetch = function () { window.__fetches.push(String(arguments[0])); return realFetch.apply(null, arguments); };
  function arm(id, spec) {
    var el = document.getElementById(id);
    window.__gofastr.action.bind(el, spec);
    return el;
  }
  window.__arm = arm;
  window.__gofastr.loadModule('action').then(function () { window.__ready = true; });
})();
</script>`

func startActionSrv(t *testing.T, body string) *actionSrv {
	t.Helper()
	js, err := RuntimeJS()
	if err != nil {
		t.Fatal(err)
	}
	s := &actionSrv{}
	mux := http.NewServeMux()
	mux.HandleFunc("/__gofastr/runtime.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte(js))
	})
	mux.HandleFunc("/__gofastr/runtime/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/__gofastr/runtime/"):]
		name = name[:len(name)-len(".js")]
		w.Header().Set("Content-Type", "application/javascript")
		if src, ok := Module(name); ok {
			w.Write([]byte(src))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		s.ok.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		s.fail.Add(1)
		http.Error(w, "no", http.StatusUnprocessableEntity)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><html><head><title>action</title></head><body>
  <main role="main"><span id="ready">ready</span>%s</main>
  <script src="/__gofastr/runtime.js"></script>
  %s
</body></html>`, body, actionArm)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func actionPage(t *testing.T, s *actionSrv) context.Context {
	t.Helper()
	ctx := chromedptest.Context(t, chromedptest.Timeout(60*time.Second))
	if err := chromedp.Run(ctx,
		chromedp.Navigate(s.srv.URL+"/"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `window.__ready === true`) {
		t.Fatal("the action primitive never loaded")
	}
	return ctx
}

const actionBtn = `<button type="button" id="b" data-state="idle">
  <span id="idle">Follow</span><span id="done" hidden>Following</span>
</button>`

// A 2xx commits: the done part is shown, the idle part hidden, and the
// committed event fired on the button.
func TestActionCommitShowsTheDonePart(t *testing.T) {
	s := startActionSrv(t, actionBtn)
	ctx := actionPage(t, s)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__arm('b', { endpoint: '/ok', idle: document.getElementById('idle'), done: document.getElementById('done') }); document.addEventListener('action:committed', function () { window.__committed = true; }, { once: true });`, nil),
		chromedp.Click(`#b`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `document.getElementById('b').getAttribute('data-state') === 'committed'`) {
		t.Fatal("the button never committed")
	}
	var doneShown, idleHidden, committed bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`!document.getElementById('done').hidden`, &doneShown),
		chromedp.Evaluate(`document.getElementById('idle').hidden`, &idleHidden),
		chromedp.Evaluate(`!!window.__committed`, &committed),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !doneShown || !idleHidden {
		t.Fatalf("label parts wrong after commit: done shown=%v idle hidden=%v", doneShown, idleHidden)
	}
	if !committed {
		t.Fatal("action:committed never fired")
	}
	if n := s.ok.Load(); n != 1 {
		t.Fatalf("endpoint hit %d times for one click, want 1", n)
	}
}

// A 422 rolls back: the rolled-back event fires, the state passes
// through error, and the 600ms timer returns the button to idle so it
// can be tried again.
func TestActionFailureRollsBackThroughError(t *testing.T) {
	s := startActionSrv(t, actionBtn)
	ctx := actionPage(t, s)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__arm('b', { endpoint: '/fail', idle: document.getElementById('idle'), done: document.getElementById('done') }); document.addEventListener('action:rolled-back', function () { window.__rolled = true; }, { once: true });`, nil),
		chromedp.Click(`#b`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `document.getElementById('b').getAttribute('data-state') === 'error'`) {
		t.Fatal("the button never entered the error state")
	}
	if !pollTrue(ctx, `window.__rolled === true`) {
		t.Fatal("action:rolled-back never fired")
	}
	if !pollTrue(ctx, `document.getElementById('b').getAttribute('data-state') === 'idle'`) {
		t.Fatal("the rollback timer never returned the button to idle")
	}
	var busyAttr string
	var disabled bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('b').getAttribute('aria-busy')`, &busyAttr),
		chromedp.Evaluate(`document.getElementById('b').disabled`, &disabled),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if busyAttr != "" || disabled {
		t.Fatalf("settlement left the button busy or disabled: aria-busy=%q disabled=%v", busyAttr, disabled)
	}
}

// Committing one member of a group revokes the sibling that was
// committed, with no second request for the sibling.
func TestActionGroupRevokesTheSibling(t *testing.T) {
	s := startActionSrv(t, `<button type="button" id="g1" data-state="committed">
  <span id="g1i" hidden>Starter</span><span id="g1d">Starter ✓</span>
</button>
<button type="button" id="g2" data-state="idle">
  <span id="g2i">Pro</span><span id="g2d" hidden>Pro ✓</span>
</button>`)
	ctx := actionPage(t, s)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__arm('g1', { endpoint: '/ok', group: 'plan', idle: document.getElementById('g1i'), done: document.getElementById('g1d') });
            window.__arm('g2', { endpoint: '/ok', group: 'plan', idle: document.getElementById('g2i'), done: document.getElementById('g2d') });`, nil),
		chromedp.Click(`#g2`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `document.getElementById('g2').getAttribute('data-state') === 'committed'`) {
		t.Fatal("the clicked button never committed")
	}
	var sibling string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('g1').getAttribute('data-state')`, &sibling),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if sibling != "idle" {
		t.Fatalf("sibling state = %q, want idle (revoked)", sibling)
	}
	if n := s.ok.Load(); n != 1 {
		t.Fatalf("endpoints hit %d times, want 1: the revoke is a local flip", n)
	}
}

// A committed button with an untoggle endpoint reverts on a second
// click, mirroring aria-pressed both ways when the spec asked for it.
func TestActionUntoggleReverts(t *testing.T) {
	s := startActionSrv(t, `<button type="button" id="t" data-state="committed" aria-pressed="true">
  <span id="ti" hidden>Watch</span><span id="td">Watching</span>
</button>`)
	ctx := actionPage(t, s)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__arm('t', { endpoint: '/ok', untoggle: '/ok', pressed: true, idle: document.getElementById('ti'), done: document.getElementById('td') }); document.addEventListener('action:untoggle', function () { window.__untoggled = true; }, { once: true });`, nil),
		chromedp.Click(`#t`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `document.getElementById('t').getAttribute('data-state') === 'idle'`) {
		t.Fatal("the committed button never reverted")
	}
	var pressed string
	var untoggled, idleShown bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('t').getAttribute('aria-pressed')`, &pressed),
		chromedp.Evaluate(`!document.getElementById('ti').hidden`, &idleShown),
		chromedp.Evaluate(`!!window.__untoggled`, &untoggled),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if pressed != "false" {
		t.Fatalf("aria-pressed = %q after untoggle, want false", pressed)
	}
	if !idleShown {
		t.Fatal("the idle part is not shown after untoggle")
	}
	if !untoggled {
		t.Fatal("action:untoggle never fired")
	}
}

// Binding twice binds once: one click, one request. The WeakSet guard
// is what keeps an adapter's scan and the kernel's insertion pass from
// arming a button twice.
func TestActionDoubleBindFiresOneRequest(t *testing.T) {
	s := startActionSrv(t, actionBtn)
	ctx := actionPage(t, s)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__arm('b', { endpoint: '/ok', idle: document.getElementById('idle'), done: document.getElementById('done') });
            window.__arm('b', { endpoint: '/ok', idle: document.getElementById('idle'), done: document.getElementById('done') });`, nil),
		chromedp.Click(`#b`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `document.getElementById('b').getAttribute('data-state') === 'committed'`) {
		t.Fatal("the button never committed")
	}
	time.Sleep(300 * time.Millisecond)
	if n := s.ok.Load(); n != 1 {
		t.Fatalf("endpoint hit %d times for one click on a twice-bound button, want 1", n)
	}
}

// A button replaced through innerHTML binds afresh and works, and the
// replaced button's rollback timer, if one was armed, touches nothing
// on the page: it writes to a detached node.
func TestActionRebindAfterSwap(t *testing.T) {
	s := startActionSrv(t, `<div id="region">`+actionBtn+`</div>`)
	ctx := actionPage(t, s)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__arm('b', { endpoint: '/fail', idle: document.getElementById('idle'), done: document.getElementById('done') });`, nil),
		chromedp.Click(`#b`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `document.getElementById('b').getAttribute('data-state') === 'error'`) {
		t.Fatal("the first button never entered the error state (its timer is what the swap must survive)")
	}
	// Replace the whole region, bind the new button to the good
	// endpoint, click it, and let the OLD button's 600ms timer fire
	// after the swap: the new button must still be committed.
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('region').innerHTML = '<button type="button" id="b2" data-state="idle"><span id="i2">Go</span><span id="d2" hidden>Gone</span></button>';
            window.__arm('b2', { endpoint: '/ok', idle: document.getElementById('i2'), done: document.getElementById('d2') });`, nil),
		chromedp.Click(`#b2`, chromedp.ByID),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `document.getElementById('b2').getAttribute('data-state') === 'committed'`) {
		t.Fatal("the replacement button never committed")
	}
	time.Sleep(700 * time.Millisecond)
	var state string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('b2').getAttribute('data-state')`, &state),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if state != "committed" {
		t.Fatalf("the replaced button's timer touched the new button: state = %q", state)
	}
}

// request refuses a cross-origin URL without fetching: the origin
// check runs before the fetch, so nothing leaves the page.
func TestActionRequestRefusesCrossOrigin(t *testing.T) {
	s := startActionSrv(t, actionBtn)
	ctx := actionPage(t, s)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__gofastr.action.request('https://elsewhere.example/mutate', 'POST')
            .then(function (ok) { window.__cross = ok; });`, nil),
	); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if !pollTrue(ctx, `window.__cross === false`) {
		t.Fatal("a cross-origin request did not resolve false")
	}
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify(window.__fetches)`, &raw)); err != nil {
		t.Fatalf("chromedp: %v", err)
	}
	if raw != "[]" {
		t.Fatalf("the refused request still fetched: %s", raw)
	}
}
