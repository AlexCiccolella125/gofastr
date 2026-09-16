// site-ping: the docs site's own registered behaviour. This is the
// first real client of the behaviour seam (docs/spec-behavior-registry.md
// sequence step 2 in miniature): the site package — NOT core-ui/runtime —
// owns the JavaScript, embeds it beside the Go that renders its markup,
// and declares its marker. The host serves it at
// /__gofastr/runtime/site-ping.js like any runtime module and the kernel
// scans for [data-site-ping] exactly as it scans its own table.
(function () {
  'use strict';
  const NAME = 'site-ping';
  const NS = window.__gofastr = window.__gofastr || {};
  function wire(el) {
    if (el.getAttribute('data-site-pinged')) return;
    el.setAttribute('data-site-pinged', '1');
    el.addEventListener('click', function () {
      const on = el.getAttribute('aria-pressed') === 'true';
      el.setAttribute('aria-pressed', on ? 'false' : 'true');
    });
  }
  function scan(root) {
    const scope = root && root.querySelectorAll ? root : document;
    if (scope.matches && scope.matches('[data-site-ping]')) wire(scope);
    const nodes = scope.querySelectorAll('[data-site-ping]');
    for (let i = 0; i < nodes.length; i++) wire(nodes[i]);
  }
  scan(document);
  NS.loadedModules = NS.loadedModules || {};
  NS.loadedModules[NAME] = true;
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
})();
