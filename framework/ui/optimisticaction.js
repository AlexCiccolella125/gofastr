// framework/ui's OptimisticAction adapter. The markup this package
// renders (data-fui-comp="ui-optimistic-action" with its endpoint,
// method, idle and success attributes), bound through the kernel's action
// primitive at core-ui/runtime/src/action.js: the primitive owns the
// mutation request and the idle → pending → committed → error machine;
// this module owns the attribute names and re-dispatches the public
// event names the component has always fired. The shake needs nothing
// here: the ui-optimistic-action stylesheet keys its animation off
// [data-state="error"], which the primitive sets.
//
// Registered by optimisticaction.go with Requires("action"), so the
// loader has the primitive registered before this file evaluates.
(() => {
  'use strict';
  const NAME = 'optimisticaction';
  const NS = window.__gofastr = window.__gofastr || {};
  // The kernel fetches a module once per page, but anything that
  // evaluates this file a second time must bind nothing twice.
  if (NS.loadedModules && Object.prototype.hasOwnProperty.call(NS.loadedModules, NAME)) return;
  // The loaded flag goes up BEFORE anything installs: a script that
  // fails halfway rejects its load and the loader drops the cached
  // promise, so a retry re-executes this file — and every listener the
  // first pass installed would be installed a second time. With the
  // flag first, the retry stops at the guard above. The trade: a
  // module that fails after this point stays "loaded", half-installed
  // rather than double-installed, which is the failure that can be
  // recovered from.
  (NS.loadedModules = NS.loadedModules || {})[NAME] = true;


  const MARKER = '[data-fui-comp="ui-optimistic-action"]';

  // The component's public event names, mirrored from the primitive's
  // action:* family so app code written against them keeps working.
  // Delegated at the document level and bound once at load, so markup
  // that arrives later (an island swap, a client navigation) is
  // covered without a listener per element.
  for (const [from, to] of [
    ['action:start', 'optimistic-action:start'],
    ['action:committed', 'optimistic-action:committed'],
    ['action:rolled-back', 'optimistic-action:rolled-back'],
  ]) {
    document.addEventListener(from, (e) => {
      const btn = e.target && e.target.closest && e.target.closest(MARKER);
      if (btn) btn.dispatchEvent(new CustomEvent(to, { bubbles: true }));
    });
  }

  // scan binds every button in scope, the scope itself included: the
  // kernel hands scan one inserted subtree and a subtree whose root is
  // the button is missed by querySelectorAll alone. Binding is the
  // primitive's to dedupe (a WeakSet there), so a second pass over the
  // same element costs a spec read and nothing else.
  function scan(root) {
    const scope = root && root.querySelectorAll ? root : document;
    const btns = scope.matches && scope.matches(MARKER) ? [scope] : [];
    for (const btn of scope.querySelectorAll(MARKER)) btns.push(btn);
    for (const btn of btns) {
      const idle = btn.querySelector('[data-fui-optimistic-idle]');
      const done = btn.querySelector('[data-fui-optimistic-success]');
      const endpoint = btn.getAttribute('data-fui-optimistic-endpoint');
      if (!idle || !done || !endpoint) continue;
      NS.action.bind(btn, {
        endpoint,
        method: btn.getAttribute('data-fui-optimistic-method') || 'POST',
        idle,
        done,
      });
    }
  }

  scan(document);
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
  NS.optimisticaction = { rescan: scan };
})();
