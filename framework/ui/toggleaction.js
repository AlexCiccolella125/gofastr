// framework/ui's ToggleAction adapter. The markup this package renders
// (data-fui-comp="ui-toggle-action" with its endpoint, method, group,
// untoggle and label attributes), bound through the kernel's action primitive at
// core-ui/runtime/src/action.js: the primitive owns the mutation
// request and the idle → pending → committed → error machine; this
// module owns the attribute names, the group key, the untoggle
// wiring and aria-pressed mirroring. The component's public events,
// toggle-action:commit and toggle-action:untoggle, are mirrored from
// the primitive's action:start and action:untoggle; a failed untoggle
// dispatches nothing, matching the primitive (the state stayed), and a
// failed commit now surfaces through the primitive's error state and
// action:rolled-back, where the old module reverted silently.
//
// Registered by toggleaction.go with Requires("action"), so the
// loader has the primitive registered before this file evaluates.
(() => {
  'use strict';
  const NAME = 'toggleaction';
  const NS = window.__gofastr = window.__gofastr || {};
  // The kernel fetches a module once per page, but anything that
  // evaluates this file a second time must bind nothing twice.
  if (NS.loadedModules && Object.prototype.hasOwnProperty.call(NS.loadedModules, NAME)) return;

  const MARKER = '[data-fui-comp="ui-toggle-action"]';

  for (const [from, to] of [
    ['action:start', 'toggle-action:commit'],
    ['action:untoggle', 'toggle-action:untoggle'],
  ]) {
    document.addEventListener(from, (e) => {
      const btn = e.target && e.target.closest && e.target.closest(MARKER);
      if (btn) btn.dispatchEvent(new CustomEvent(to, { bubbles: true }));
    });
  }

  // scan binds every button in scope, the scope itself included (see
  // the optimistic adapter for why). Binding is the primitive's to
  // dedupe, so a second pass over the same element is a spec read.
  function scan(root) {
    const scope = root && root.querySelectorAll ? root : document;
    const btns = scope.matches && scope.matches(MARKER) ? [scope] : [];
    for (const btn of scope.querySelectorAll(MARKER)) btns.push(btn);
    for (const btn of btns) {
      const idle = btn.querySelector('[data-fui-toggle-idle]');
      const done = btn.querySelector('[data-fui-toggle-committed]');
      const endpoint = btn.getAttribute('data-fui-toggle-endpoint');
      if (!idle || !done || !endpoint) continue;
      // allow-untoggle arms the revert path even without an endpoint
      // of its own: an empty untoggle string is a local flip with no
      // request, which is the component's documented behaviour, and an
      // absent one leaves the button sticky.
      const allow = (btn.getAttribute('data-fui-toggle-allow-untoggle') || '').toLowerCase() === 'true';
      const spec = {
        endpoint,
        method: btn.getAttribute('data-fui-toggle-method') || 'POST',
        idle,
        done,
        // The committed state is the pressed-toggle convention; the
        // component ships aria-pressed and the primitive mirrors it.
        pressed: true,
      };
      const group = btn.getAttribute('data-fui-toggle-group');
      if (group) spec.group = group;
      if (allow) spec.untoggle = btn.getAttribute('data-fui-toggle-untoggle-endpoint') || '';
      NS.action.bind(btn, spec);
    }
  }

  scan(document);
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
  NS.toggleaction = { rescan: scan };
  (NS.loadedModules = NS.loadedModules || {})[NAME] = true;
})();
