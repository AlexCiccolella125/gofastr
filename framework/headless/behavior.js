// headless: the behaviour module for this package's data-hui-* hooks.
// The package that renders the markup owns the JavaScript that binds
// it, the way it owns the stylesheet that styles it: the host serves
// this file as the runtime module "headless" at
// /__gofastr/runtime/headless.js, and the kernel loads it when one of
// its markers is on the page, at boot, on DOM insertion, or after a
// client navigation, handing every inserted subtree and every
// post-navigation document to scan() below. Nothing here observes the
// DOM on its own: a MutationObserver or a navigate listener of our
// own would arm everything a second time on top of the kernel's pass.
//
// Every sentence this module writes arrived as a data-hui-* attribute
// the component rendered from its Words, so a translated page
// announces in its own language. The only attributes it writes back
// are its own runtime-owned hooks, data-hui-when-off and
// data-hui-drop-over, which no component renders.
(function () {
  'use strict';
  const NAME = 'headless';
  const NS = window.__gofastr = window.__gofastr || {};
  // The kernel fetches a module once per page, but anything that
  // evaluates this file a second time must bind nothing twice.
  if (NS.loadedModules && Object.prototype.hasOwnProperty.call(NS.loadedModules, NAME)) return;

  const HEX = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i;
  const DISMISSED_KEY = 'gofastr.headless.system.dismissed';

  // within(root, sel): root itself when it matches, plus everything
  // matching inside it. The kernel hands scan() one inserted subtree,
  // and a subtree whose root IS the marker is missed by
  // querySelectorAll alone.
  function within(root, sel) {
    const out = [];
    if (root.matches && root.matches(sel)) out.push(root);
    if (root.querySelectorAll) {
      const found = root.querySelectorAll(sel);
      for (let i = 0; i < found.length; i++) out.push(found[i]);
    }
    return out;
  }

  // once(el, kind) is the arrival pass's idempotence guard. The same
  // element reaches scan() more than once (an island swap inside an
  // already-scanned subtree, the post-navigation document pass), and
  // whatever binds a listener on arrival must not bind it twice.
  const armedFor = new WeakMap();
  function once(el, kind) {
    let kinds = armedFor.get(el);
    if (!kinds) {
      kinds = new Set();
      armedFor.set(el, kinds);
    }
    if (kinds.has(kind)) return false;
    kinds.add(kind);
    return true;
  }

  // ─── reveal (Password) ──────────────────────────────────────────

  // reveal retypes the input and swaps both the button's visible text
  // and its accessible name, so both say what the button will do NEXT.
  // All four strings came from the component's words as data-*, which
  // is why none is said here. The caret is kept where the reader left
  // it: a reveal that costs the typing position fights the person
  // using it.
  function reveal(btn) {
    const shell = btn.closest('[data-hui-affix]');
    const input = shell && shell.querySelector('[data-hui-affix-input]');
    if (!input) return;
    const shown = input.type === 'text';
    input.type = shown ? 'password' : 'text';
    btn.setAttribute('aria-pressed', String(!shown));
    btn.setAttribute('aria-label', shown ? btn.dataset.huiShowLabel : btn.dataset.huiHideLabel);
    btn.textContent = shown ? btn.dataset.huiShowText : btn.dataset.huiHideText;
    const at = input.value.length;
    input.focus();
    try { input.setSelectionRange(at, at); } catch (e) { /* type=password forbids it in Safari */ }
  }

  // ─── colour (Color) ─────────────────────────────────────────────

  // syncColour keeps the swatch and the hex text one value. From the
  // swatch the text takes the uppercase hex; from the text the swatch
  // takes the expanded #rgb or #rrggbb. A non-empty value that is
  // neither marks the shell and stays verbatim in the text: the field
  // edits config, and rewriting an unpickable value to black would
  // destroy it.
  function syncColour(el) {
    const shell = el.closest('[data-hui-affix]');
    if (!shell) return;
    const swatch = shell.querySelector('[data-hui-affix-swatch]');
    const text = shell.querySelector('[data-hui-affix-input]');
    if (!swatch || !text) return;
    if (el === swatch) {
      text.value = swatch.value.toUpperCase();
      shell.removeAttribute('data-invalid');
      return;
    }
    const v = text.value.trim();
    if (HEX.test(v)) {
      swatch.value = v.length === 4 ? '#' + v[1] + v[1] + v[2] + v[2] + v[3] + v[3] : v;
      shell.removeAttribute('data-invalid');
    } else if (v !== '') {
      shell.setAttribute('data-invalid', '');
    }
  }

  // ─── when (ConditionalField) ────────────────────────────────────

  // whenValue reads the watched field's value the way the form would
  // submit it: the checked radio's value, a checkbox's value when
  // checked and the empty string when not, and any other control's
  // value.
  function whenValue(scope, name) {
    const fields = scope.querySelectorAll('[name="' + CSS.escape(name) + '"]');
    for (let i = 0; i < fields.length; i++) {
      const el = fields[i];
      if (el.type === 'radio') {
        if (el.checked) return el.value;
        continue;
      }
      if (el.type === 'checkbox') return el.checked ? el.value : '';
      return el.value;
    }
    return '';
  }

  function syncWhen(region) {
    const scope = region.closest('form') || document;
    const shown = whenValue(scope, region.dataset.huiWhen) === region.dataset.huiWhenValue;
    region.hidden = !shown;
    const controls = region.querySelectorAll('input, select, textarea, button');
    for (let i = 0; i < controls.length; i++) {
      const c = controls[i];
      // The mark is what tells a control we disabled from one the page
      // disabled itself, so showing the region re-enables exactly the
      // controls that hiding it disabled.
      if (!shown && !c.disabled) {
        c.disabled = true;
        c.dataset.huiWhenOff = '';
      } else if (shown && c.dataset.huiWhenOff !== undefined) {
        c.disabled = false;
        delete c.dataset.huiWhenOff;
      }
    }
  }

  // ─── form errors (Form with a ValidationSummary) ────────────────

  // A failed submit is announced by moving focus to the summary, once
  // per form element. A swap that brings the SAME form back (an island
  // re-render of the region around it) must not steal focus again; a
  // NEW form element with the same errors is focused, because a reader
  // who submitted again and failed again has to be told.
  function armFormErrors(root) {
    const forms = within(root, '[data-hui-form-errors]');
    // Every form in the pass is marked, and only the first summary is
    // focused: a form left unmarked because an earlier one took the
    // focus would take it itself on the next pass, from wherever the
    // reader had moved to by then.
    let announced = false;
    for (let i = 0; i < forms.length; i++) {
      const form = forms[i];
      if (!once(form, 'errors')) continue;
      const summary = form.querySelector('[role="alert"][tabindex="-1"]');
      if (summary && !announced) {
        summary.focus();
        announced = true;
      }
    }
  }

  // ─── action (OptimisticAction / ToggleAction) ───────────────────

  // The framework's optimistic runtime rolls a failed mutation back
  // and dispatches optimistic-action:rolled-back from the button, but
  // says nothing while doing so. This writes the sentence the
  // component carried on its root into the polite status span inside
  // it, one frame after clearing it, so a reader who already heard a
  // previous sentence hears this one as new. The toggle module
  // dispatches nothing on failure; its rollback stays silent here on
  // purpose until it grows an event of its own.
  function announceFailure(btn) {
    const status = btn.querySelector('[data-hui-action-status]');
    if (!status) return;
    const text = btn.dataset.huiActionFailed || '';
    status.textContent = '';
    requestAnimationFrame(function () { status.textContent = text; });
  }

  // ─── drop (FileUpload) ──────────────────────────────────────────

  // showFiles lists the chosen names and says the sentence, both built
  // from the words the component rendered on the root:
  // data-hui-drop-one for a single file with {name}, data-hui-drop-many
  // for several with {n} and {names}. An empty selection clears both,
  // so changing one's mind leaves nothing behind.
  function showFiles(root) {
    const input = document.getElementById(root.dataset.huiDropInput);
    const list = root.querySelector('[data-hui-drop-list]');
    const status = root.querySelector('[data-hui-drop-status]');
    if (!input || !list) return;
    list.textContent = '';
    const files = input.files || [];
    const names = [];
    for (let i = 0; i < files.length; i++) {
      const li = document.createElement('li');
      li.textContent = files[i].name;
      list.appendChild(li);
      names.push(files[i].name);
    }
    if (!status) return;
    if (files.length === 1) {
      status.textContent = (root.dataset.huiDropOne || '').replace('{name}', files[0].name);
    } else if (files.length > 1) {
      status.textContent = (root.dataset.huiDropMany || '')
        .replace('{n}', String(files.length))
        .replace('{names}', names.join(', '));
    } else {
      status.textContent = '';
    }
  }

  function armDrop(root) {
    const input = document.getElementById(root.dataset.huiDropInput);
    if (!input || !once(root, 'drop')) return;
    function stop(e) { e.preventDefault(); e.stopPropagation(); }
    root.addEventListener('dragenter', function (e) { stop(e); root.dataset.huiDropOver = ''; });
    root.addEventListener('dragover', function (e) { stop(e); root.dataset.huiDropOver = ''; });
    root.addEventListener('dragleave', function (e) { stop(e); delete root.dataset.huiDropOver; });
    root.addEventListener('drop', function (e) {
      stop(e);
      delete root.dataset.huiDropOver;
      // A disabled input keeps its files: the drop is refused rather
      // than silently queued for a control that cannot submit.
      if (input.disabled || !e.dataTransfer) return;
      // The picker lets one file through an input without multiple;
      // a drop keeps the same rule rather than smuggling several past
      // it. The first file is the one taken, as the picker would take
      // the one chosen.
      let dropped = e.dataTransfer.files;
      if (!input.multiple && dropped.length > 1) {
        const one = new DataTransfer();
        one.items.add(dropped[0]);
        dropped = one.files;
      }
      input.files = dropped;
      showFiles(root);
      input.dispatchEvent(new Event('change', { bubbles: true }));
    });
  }

  function armDrops(root) {
    const roots = within(root, '[data-hui-drop]');
    for (let i = 0; i < roots.length; i++) armDrop(roots[i]);
  }

  // ─── system banners (SystemBanner) ──────────────────────────────

  // A dismissed system message is remembered for the session, so an
  // island swap that re-renders it does not say it twice. The store
  // can be refused (private mode, policy): every access is guarded and
  // the worst case is a message shown again.
  const systemDismissed = new Set();
  try {
    const stored = JSON.parse(sessionStorage.getItem(DISMISSED_KEY) || '[]');
    for (let s = 0; s < stored.length; s++) systemDismissed.add(stored[s]);
  } catch (e) { /* no storage: dismissals last until the page does */ }

  function rememberDismissal(id) {
    systemDismissed.add(id);
    try {
      const ids = [];
      systemDismissed.forEach(function (v) { ids.push(v); });
      sessionStorage.setItem(DISMISSED_KEY, JSON.stringify(ids));
    } catch (e) { /* no storage: nothing to remember with */ }
  }

  function armSystem(root) {
    const banners = within(root, '[data-hui-system]');
    for (let i = 0; i < banners.length; i++) {
      const el = banners[i];
      if (!el.hidden && systemDismissed.has(el.dataset.huiSystemId)) el.hidden = true;
    }
  }

  // ─── delegated listeners ────────────────────────────────────────

  // Clicks, typing and the two custom events are delegated from the
  // document and bound once at load, which is why markup that arrives
  // later (an island swap, a client navigation) needs no re-binding
  // for them: the listener was never on the element.
  document.addEventListener('click', function (e) {
    const t = e.target;
    if (!t || !t.closest) return;
    const btn = t.closest('[data-hui-reveal]');
    if (btn) {
      e.preventDefault();
      reveal(btn);
      return;
    }
    const dismiss = t.closest('[data-hui-system-dismiss]');
    if (dismiss) {
      const el = dismiss.closest('[data-hui-system]');
      if (!el) return;
      e.preventDefault();
      el.hidden = true;
      if (el.dataset.huiSystemId) rememberDismissal(el.dataset.huiSystemId);
    }
  });

  document.addEventListener('input', function (e) {
    const t = e.target;
    if (!t || !t.closest) return;
    if (t.matches('[data-hui-affix-swatch], [data-hui-color] [data-hui-affix-input]')) syncColour(t);
    const regions = (t.form || document).querySelectorAll('[data-hui-when]');
    for (let i = 0; i < regions.length; i++) syncWhen(regions[i]);
  });

  document.addEventListener('change', function (e) {
    const t = e.target;
    if (!t || !t.closest) return;
    const root = t.closest('[data-hui-drop]');
    if (root && t.type === 'file') showFiles(root);
    const regions = (t.form || document).querySelectorAll('[data-hui-when]');
    for (let i = 0; i < regions.length; i++) syncWhen(regions[i]);
  });

  document.addEventListener('optimistic-action:rolled-back', function (e) {
    const btn = e.target && e.target.closest && e.target.closest('[data-hui-action]');
    if (btn) announceFailure(btn);
  });

  // The offline banner follows the connection the framework reports:
  // shown once a retry is actually scheduled (a blip during the first
  // connect is not an outage), hidden when the link is back. The
  // dismissed set does not apply here, because the banner has no
  // dismiss memory of its own: losing the connection again must show
  // it again.
  document.addEventListener('gofastr:sse-status', function (e) {
    const status = (e && e.detail) || {};
    const lost = status.connected === false && status.retryCount > 0;
    const banners = document.querySelectorAll('[data-hui-system-offline]');
    for (let i = 0; i < banners.length; i++) banners[i].hidden = !lost;
  });

  // ─── the arrival pass ───────────────────────────────────────────

  // scan arms what arrival alone cannot: the summary focus, the drag
  // listeners, the when-regions' first sync, the dismissed banners.
  // It is what the kernel calls on every inserted subtree and over the
  // document after a client navigation, and it is idempotent through
  // the once() guard above.
  function scan(root) {
    const scope = root && root.querySelectorAll ? root : document;
    armFormErrors(scope);
    armDrops(scope);
    const regions = within(scope, '[data-hui-when]');
    for (let i = 0; i < regions.length; i++) syncWhen(regions[i]);
    armSystem(scope);
  }

  scan(document);
  NS.loadedModules = NS.loadedModules || {};
  NS.loadedModules[NAME] = true;
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
})();
