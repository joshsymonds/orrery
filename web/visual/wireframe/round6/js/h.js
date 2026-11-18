// Round 6 wireframe: helpers. A component is a function that returns a string of HTML, so the desk page, the phone page
// and the catalogue all draw one component from one definition.
(() => {
  'use strict';
  window.O = window.O || {};

  const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' };
  // esc makes text safe to place in markup.
  O.esc = (text) => String(text ?? '').replace(/[&<>"]/g, (c) => ESC[c]);

  // cls joins the class names that are set: cls('row', cursor && 'cur').
  O.cls = (...names) => names.flat().filter(Boolean).join(' ');

  // attrs writes an attribute for each key with a value; true writes the bare name.
  O.attrs = (map) => Object.entries(map || {})
    .filter(([, v]) => v !== undefined && v !== null && v !== false && v !== '')
    .map(([k, v]) => (v === true ? ` ${k}` : ` ${k}="${O.esc(v)}"`))
    .join('');

  // each draws a list with one function and joins the parts.
  O.each = (list, draw) => (list || []).map(draw).join('');

  // when draws its second argument only when the first holds.
  O.when = (test, html) => (test ? (typeof html === 'function' ? html() : html) : '');

  // md draws the small Markdown a fixture uses: **bold**, *italic*, `code`, [text](to). Text is escaped first.
  O.md = (text) => O.esc(text)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\*([^*]+)\*/g, '<em>$1</em>')
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a class="link" href="$2">$1</a>');

  // go is the href that opens a scenario in the frame it is drawn in.
  O.go = (id) => `#${id}`;

  // The page a script runs in: 'desk', 'phone', 'catalog' or 'board'.
  O.frame = document.documentElement.dataset.frame || 'desk';
  O.touch = () => O.frame === 'phone';
})();
