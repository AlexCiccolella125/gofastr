// The page script: it only paints. Every record it touches goes
// through the store the Go declaration generated, so the caps, the
// namespace and the schema version are the declaration's, not this
// file's. Served on the extra-script rail after runtime.js and the
// store's manifest (no inline JavaScript).
(() => {
  'use strict';
  const G = window.__gofastr;
  const APP = 'team-builder';
  const SLOTS = 6;
  let wired = null;

  const wire = () => {
    const list = document.getElementById('roster');
    if (!list || wired === list) return;
    wired = list;
    G.loadModule('local-store').then(() => {
      const store = G.localStore(APP);
      const teams = store.collection('teams');
      const verdicts = store.collection('verdicts');
      const nameEl = document.getElementById('member-name');
      const roleEl = document.getElementById('member-role');
      const count = document.getElementById('roster-count');
      const last = document.getElementById('last-verdict');

      // The roster, from the record. The record is the truth; the DOM
      // is repainted from it on every change, including one made in
      // another tab or written back by the server.
      const paint = (team) => {
        const members = (team && Array.isArray(team.members)) ? team.members : [];
        list.textContent = '';
        for (const m of members) {
          const li = document.createElement('li');
          li.textContent = m.name + ' — ' + m.role;
          list.appendChild(li);
        }
        count.textContent = members.length
          ? members.length + ' of ' + SLOTS + ' slots filled.'
          : 'No members yet.';
      };
      const load = () => teams.get('current').then(paint);
      const paintVerdict = () => verdicts.get('latest').then((v) => {
        last.textContent = v ? 'Last verdict, kept in this browser: ' + v.text : 'No verdict kept yet.';
      });

      load();
      paintVerdict();
      teams.subscribe(load);
      verdicts.subscribe(paintVerdict);

      document.getElementById('add-member').addEventListener('click', () => {
        const name = nameEl.value.trim();
        if (!name) { nameEl.focus(); return; }
        teams.get('current').then((t) => {
          const members = ((t && t.members) || []).concat([{ name: name, role: roleEl.value }]);
          return teams.put({ id: 'current', members: members });
        }).then((r) => {
          if (!r.ok) { console.warn('[team-builder] not saved:', r.reason); return; }
          nameEl.value = '';
          nameEl.focus();
        });
      });
      document.getElementById('clear-team').addEventListener('click', () => {
        teams.delete('current');
      });
    });
  };

  wire();
  window.addEventListener('gofastr:navigate', () => { wired = null; wire(); });
})();
