import test from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><body></body>', { url: 'http://localhost' });
for (const name of ['window', 'document', 'Node', 'NodeFilter', 'HTMLElement', 'Element', 'MutationObserver', 'getComputedStyle']) {
  globalThis[name] = name === 'getComputedStyle' ? dom.window.getComputedStyle.bind(dom.window) : dom.window[name];
}
Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true });
const { renderMarkdown, createDocument, highlightSearch } = await import('../frontend/notes-editor.js');

test('Markdown and legacy code render safely with preserved line breaks', () => {
  const html = renderMarkdown('# Titel\n\n**Fett** und *kursiv*\nZeile zwei\n\n- Eins\n- Zwei\n\n{code}\n<script>alert(1)</script>\n```\n{code}');
  assert.match(html, /<h1>Titel<\/h1>/);
  assert.match(html, /<strong>Fett<\/strong>/);
  assert.match(html, /<br>Zeile zwei/);
  assert.match(html, /<ul>/);
  assert.match(html, /&lt;script&gt;alert\(1\)&lt;\/script&gt;/);
  assert.doesNotMatch(html, /<script/);
  assert.match(html, /```/);
});

test('Links honor the clickable option and disallow unsafe protocols', () => {
  assert.match(renderMarkdown('[Gut](https://example.com)'), /rel="noopener noreferrer"/);
  assert.doesNotMatch(renderMarkdown('[Gut](https://example.com)', false), /<a/);
  assert.doesNotMatch(renderMarkdown('[Nein](javascript:alert%281%29)'), /href=/);
  assert.doesNotMatch(renderMarkdown('<img src=x onerror=alert(1)>'), /<img/);
});

test('Editor keeps untouched source and serializes actual changes including empty content', () => {
  const element = document.body.appendChild(document.createElement('div'));
  const source = '# Titel\n\n**Text**\nNeue Zeile';
  const adapter = createDocument(element, source);
  assert.equal(adapter.getContent(), source);
  assert.equal(adapter.editor.getText(), 'Titel\n\nText\nNeue Zeile');
  adapter.editor.commands.setContent('<h2>Neu</h2><p><strong>Fett</strong></p>');
  assert.match(adapter.getContent(), /## Neu/);
  assert.match(adapter.getContent(), /\*\*Fett\*\*/);
  adapter.editor.commands.clearContent();
  assert.equal(adapter.getContent(), '');
  adapter.destroy();
  element.remove();
});

test('Task editor preserves completed steps, formatting and checklist Markdown', () => {
  const element = document.body.appendChild(document.createElement('div'));
  const source = '# Abschnitt\n- [x] Erledigt\n- [ ] Offen\n\n## Morgen\n- [ ] **Wichtig**';
  const adapter = createDocument(element, source);
  assert.equal(adapter.getContent(), source);
  assert.equal(element.querySelectorAll('li[data-type="taskItem"]').length, 3);
  assert.equal(element.querySelectorAll('input:checked').length, 1);
  adapter.editor.commands.insertContentAt(2, ' Neu');
  const saved = adapter.getContent();
  assert.match(saved, /- \[x\] Erledigt/);
  assert.match(saved, /- \[ \] Offen/);
  assert.match(saved, /## Morgen/);
  assert.match(saved, /\*\*Wichtig/);
  assert.equal((saved.match(/^- \[[ x]\]/gm) || []).length, 3);
  adapter.destroy();
  element.remove();
});

test('Highlights span inline formatting, normalize accents and clear without changing text', () => {
  const root = document.createElement('div');
  root.innerHTML = '<p>Git<strong>Hub</strong> und Café, 100%.</p>';
  const text = root.textContent;
  highlightSearch(root, 'github cafe 100%');
  assert.equal(root.querySelectorAll('mark').length, 4);
  assert.equal(root.textContent, text);
  assert.equal(root.querySelector('strong').textContent, 'Hub');
  highlightSearch(root, 'Hub');
  assert.equal(root.querySelectorAll('mark').length, 1);
  highlightSearch(root, '');
  assert.equal(root.querySelectorAll('mark').length, 0);
  assert.equal(root.textContent, text);
});

test('Supported formatting survives editor serialization and the read view', () => {
  const element = document.body.appendChild(document.createElement('div'));
  const source = '# Plan\n\n## Heute\n\n### Details\n\n> Wichtig\n\n1. Erster Schritt\n2. Zweiter Schritt\n\n- **Fett** und *kursiv* mit `code`\n\n[Link](https://example.com)\n\n```js\nconst value = 1;\n```\n\n---\n\nZeile eins\nZeile zwei';
  const adapter = createDocument(element, source);
  adapter.editor.commands.insertContentAt(adapter.editor.state.doc.content.size - 1, ' Ende');
  const html = renderMarkdown(adapter.getContent());
  for (const tag of ['h1', 'h2', 'h3', 'blockquote', 'ol', 'ul', 'strong', 'em', 'code', 'a', 'pre', 'hr', 'br']) assert.match(html, new RegExp(`<${tag}[ >]`));
  assert.match(html, /Zeile zwei Ende/);
  adapter.destroy();
  element.remove();
});

test('Highlight overlap, repeated matches and literal special characters do not alter links', () => {
  const root = document.createElement('div');
  root.innerHTML = '<p><a href="https://example.com">ababa [x] 100% Café</a></p>';
  highlightSearch(root, 'aba bab [x] 100% cafe');
  assert.equal(root.querySelectorAll('mark').length, 4);
  assert.equal(root.querySelector('a').getAttribute('href'), 'https://example.com');
  highlightSearch(root, 'ab');
  assert.equal(root.querySelectorAll('mark').length, 2);
  highlightSearch(root, '');
  assert.equal(root.innerHTML, '<p><a href="https://example.com">ababa [x] 100% Café</a></p>');
});

test('Empty checklist steps remain visible and keep their checked state', () => {
  const element = document.body.appendChild(document.createElement('div'));
  const adapter = createDocument(element, '- [ ]\n- [x]');
  assert.equal(element.querySelectorAll('li[data-type="taskItem"]').length, 2);
  assert.equal(element.querySelectorAll('input:checked').length, 1);
  adapter.destroy();
  element.remove();
});