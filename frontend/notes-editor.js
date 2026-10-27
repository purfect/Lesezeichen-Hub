import { Editor } from '@tiptap/core';
import StarterKit from '@tiptap/starter-kit';
import { Markdown } from '@tiptap/markdown';
import { TaskList, TaskItem } from '@tiptap/extension-list';
import DOMPurify from 'dompurify';
import { Marked } from 'marked';
import { createElement as createIcon, Bold, Italic, List, ListOrdered, ListChecks, Quote, Link, Code, SquareCode, Undo2, Redo2, X, Check } from 'lucide';

const escapeText = text => String(text).replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
const safeURL = value => {
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) ? url.href : null;
  } catch {
    return null;
  }
};

const parser = new Marked({
  breaks: true,
  gfm: true,
  renderer: {
    checkbox: () => '',
    list(token) {
      let html = '';
      let openTag = '';
      token.items.forEach((item, index) => {
        const emptyTask = !token.ordered && /^\[[ xX]\]\s*$/.test(item.text);
        const task = item.task || emptyTask;
        const checked = emptyTask ? /^\[[xX]\]/.test(item.text) : Boolean(item.checked);
        const tag = task ? 'task' : token.ordered ? 'ol' : 'ul';
        if (tag !== openTag) {
          if (openTag) html += openTag === 'ol' ? '</ol>' : '</ul>';
          html += tag === 'task' ? '<ul data-type="taskList">' : tag === 'ol' ? `<ol start="${Number(token.start) + index}">` : '<ul>';
          openTag = tag;
        }
        const body = emptyTask ? '<p></p>' : this.parser.parse(item.tokens);
        html += task
          ? `<li data-type="taskItem" data-checked="${checked}"><label><input type="checkbox" disabled${checked ? ' checked' : ''}><span></span></label><div>${body || '<p></p>'}</div></li>`
          : `<li>${body}</li>`;
      });
      return html + (openTag === 'ol' ? '</ol>' : '</ul>');
    },
    html: token => escapeText(token.text),
    image: token => escapeText(token.raw),
    table: token => `<pre><code>${escapeText(token.raw)}</code></pre>`,
    link(token) {
      const label = this.parser.parseInline(token.tokens);
      const href = safeURL(token.href);
      return href ? `<a href="${escapeText(href)}" target="_blank" rel="noopener noreferrer">${label}</a>` : label;
    },
  },
});

export function legacyMarkdown(source) {
  return String(source || '').replace(/\{code\}([\s\S]*?)\{code\}/g, (_match, code) => {
    const runs = code.match(/`+/g) || [];
    const fence = '`'.repeat(Math.max(3, ...runs.map(run => run.length + 1)));
    return `\n\n${fence}\n${code.replace(/^\r?\n/, '').replace(/\r?\n$/, '')}\n${fence}\n\n`;
  });
}

export function renderMarkdown(source, linksClickable = true) {
  const html = parser.parse(legacyMarkdown(source));
  const clean = DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ['p', 'br', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'strong', 'em', 's', 'del', 'ul', 'ol', 'li', 'blockquote', 'pre', 'code', 'a', 'hr', 'label', 'input', 'div', 'span'],
    ALLOWED_ATTR: ['href', 'target', 'rel', 'start', 'data-type', 'data-checked', 'type', 'checked', 'disabled'],
    ALLOW_DATA_ATTR: false,
  });
  const template = document.createElement('template');
  template.innerHTML = clean;
  for (const link of template.content.querySelectorAll('a')) {
    if (!linksClickable || !safeURL(link.getAttribute('href'))) {
      link.replaceWith(...link.childNodes);
    } else {
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
    }
  }
  return template.innerHTML;
}

export function createDocument(element, source, onUpdate) {
  let changed = false;
  const editor = new Editor({
    element,
    extensions: [StarterKit.configure({
      underline: false,
      link: { openOnClick: false, protocols: ['http', 'https'], isAllowedUri: url => Boolean(safeURL(url)) },
    }), TaskList, TaskItem.configure({
      nested: true,
      HTMLAttributes: { 'data-type': 'taskItem' },
      a11y: { checkboxLabel: node => `Aufgabe: ${node.textContent || 'Neuer Schritt'}` },
    }), Markdown],
    content: renderMarkdown(source),
    editorProps: { attributes: { class: 'note-rich-content', role: 'textbox', 'aria-label': 'Inhalt der Notiz', 'aria-multiline': 'true' } },
    onUpdate() {
      changed = true;
      onUpdate?.();
    },
  });
  return {
    editor,
    getContent: () => changed ? editor.getMarkdown() : source,
    destroy: () => editor.destroy(),
  };
}

export function mountEditor(container, source, onUpdate) {
  container.classList.add('note-rich-editor');
  const toolbar = document.createElement('div');
  toolbar.className = 'note-format-toolbar';
  toolbar.setAttribute('role', 'group');
  toolbar.setAttribute('aria-label', 'Textformatierung');
  const heading = document.createElement('select');
  heading.setAttribute('aria-label', 'Absatzformat');
  heading.title = 'Absatzformat';
  for (const [value, label] of [['0', 'Absatz'], ['1', 'Überschrift 1'], ['2', 'Überschrift 2'], ['3', 'Überschrift 3']]) {
    heading.add(new Option(label, value));
  }
  toolbar.append(heading);
  const content = document.createElement('div');
  container.append(toolbar, content);
  const adapter = createDocument(content, source, onUpdate);
  const editor = adapter.editor;
  const controls = [];
  function button(label, icon, action, active, enabled) {
    const control = document.createElement('button');
    control.type = 'button';
    control.title = label;
    control.setAttribute('aria-label', label);
    control.append(createIcon(icon, { width: 18, height: 18, 'aria-hidden': 'true' }));
    control.addEventListener('mousedown', event => event.preventDefault());
    control.addEventListener('click', action);
    toolbar.append(control);
    controls.push({ control, active, enabled });
    return control;
  }
  heading.addEventListener('change', () => {
    const level = Number(heading.value);
    const selection = document.getSelection();
    const chain = editor.chain().focus();
    if (selection?.rangeCount && editor.view.dom.contains(selection.anchorNode) && editor.view.dom.contains(selection.focusNode)) {
      const anchor = editor.view.posAtDOM(selection.anchorNode, selection.anchorOffset);
      const focus = editor.view.posAtDOM(selection.focusNode, selection.focusOffset);
      chain.setTextSelection({ from: Math.min(anchor, focus), to: Math.max(anchor, focus) });
    }
    if (level) chain.setHeading({ level }).run();
    else chain.setParagraph().run();
  });
  button('Fett', Bold, () => editor.chain().focus().toggleBold().run(), () => editor.isActive('bold'));
  button('Kursiv', Italic, () => editor.chain().focus().toggleItalic().run(), () => editor.isActive('italic'));
  button('Aufzählung', List, () => editor.chain().focus().toggleBulletList().run(), () => editor.isActive('bulletList'));
  button('Nummerierung', ListOrdered, () => editor.chain().focus().toggleOrderedList().run(), () => editor.isActive('orderedList'));
  button('Aufgabenliste', ListChecks, () => editor.chain().focus().toggleTaskList().run(), () => editor.isActive('taskList'));
  button('Zitat', Quote, () => editor.chain().focus().toggleBlockquote().run(), () => editor.isActive('blockquote'));

  const dialog = document.createElement('dialog');
  dialog.className = 'note-link-dialog';
  dialog.setAttribute('aria-labelledby', 'note-link-title');
  dialog.innerHTML = '<h3 id="note-link-title">Link bearbeiten</h3><label for="note-link-url">URL</label><input id="note-link-url" type="text" inputmode="url" autocomplete="off" placeholder="https://" /><p class="note-link-error" role="alert"></p><div class="note-link-actions"></div>';
  container.append(dialog);
  const urlInput = dialog.querySelector('input');
  const error = dialog.querySelector('.note-link-error');
  const actions = dialog.querySelector('.note-link-actions');
  function dialogButton(label, icon, action) {
    const control = document.createElement('button');
    control.type = 'button';
    control.append(createIcon(icon, { width: 16, height: 16, 'aria-hidden': 'true' }), document.createTextNode(label));
    control.addEventListener('click', action);
    actions.append(control);
  }
  function applyLink() {
    const href = safeURL(urlInput.value.trim());
    if (!href) {
      error.textContent = 'Bitte eine vollständige HTTP- oder HTTPS-Adresse eingeben.';
      urlInput.focus();
      return;
    }
    dialog.close();
    const chain = editor.chain().focus().extendMarkRange('link');
    if (editor.state.selection.empty && !editor.isActive('link')) chain.insertContent({ type: 'text', text: href, marks: [{ type: 'link', attrs: { href } }] }).run();
    else chain.setLink({ href }).run();
  }
  dialogButton('Abbrechen', X, () => dialog.close());
  dialogButton('Link entfernen', X, () => { dialog.close(); editor.chain().focus().extendMarkRange('link').unsetLink().run(); });
  dialogButton('Übernehmen', Check, applyLink);
  urlInput.addEventListener('keydown', event => {
    if (event.key === 'Enter') { event.preventDefault(); applyLink(); }
  });
  dialog.addEventListener('close', () => editor.commands.focus());
  button('Link', Link, () => {
    urlInput.value = editor.getAttributes('link').href || '';
    error.textContent = '';
    dialog.showModal();
    urlInput.focus();
    urlInput.select();
  }, () => editor.isActive('link'));
  button('Inline-Code', Code, () => editor.chain().focus().toggleCode().run(), () => editor.isActive('code'));
  button('Codeblock', SquareCode, () => editor.chain().focus().toggleCodeBlock().run(), () => editor.isActive('codeBlock'));
  button('Rückgängig', Undo2, () => editor.chain().focus().undo().run(), null, () => editor.can().undo());
  button('Wiederholen', Redo2, () => editor.chain().focus().redo().run(), null, () => editor.can().redo());
  function updateToolbar() {
    heading.value = String(editor.isActive('heading') ? editor.getAttributes('heading').level : 0);
    for (const { control, active, enabled } of controls) {
      if (active) control.setAttribute('aria-pressed', String(active()));
      if (enabled) control.disabled = !enabled();
    }
  }
  editor.on('transaction', updateToolbar);
  updateToolbar();
  return {
    ...adapter,
    addTask() {
      const position = editor.state.doc.content.size;
      editor.chain().focus().insertContentAt(position, {
        type: 'taskList',
        content: [{ type: 'taskItem', attrs: { checked: false }, content: [{ type: 'paragraph' }] }],
      }).setTextSelection(position + 3).run();
    },
    destroy() {
      adapter.destroy();
      container.replaceChildren();
    },
  };
}

export function highlightSearch(root, query) {
  for (const mark of root.querySelectorAll('mark.note-search-hit')) mark.replaceWith(...mark.childNodes);
  root.normalize();
  const terms = [...new Set(String(query || '').trim().split(/\s+/).filter(Boolean))];
  if (!terms.length) return;
  const fold = (value, accents) => accents
    ? value.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase()
    : value.replace(/[A-Z]/g, character => character.toLowerCase());
  const blocks = root.querySelectorAll('p, h1, h2, h3, h4, h5, h6, li, pre, .note-view-content:not(.note-markdown), .checklist-group-title, .checklist-item-text, .checklist-text-line, .note-tag, .note-item-title, #detail-title-label');
  for (const block of blocks) {
    if (block.querySelector('p, li, pre, .checklist-item-text')) continue;
    const walker = document.createTreeWalker(block, NodeFilter.SHOW_TEXT);
    const nodes = [];
    let node;
    let text = '';
    while ((node = walker.nextNode())) {
      nodes.push({ node, start: text.length, end: text.length + node.length });
      text += node.textContent;
    }
    const hits = [];
    for (const term of terms) {
      const accents = [...term].length >= 3;
      let normalized = '';
      const positions = [];
      let offset = 0;
      for (const character of text) {
        const folded = fold(character, accents);
        for (let index = 0; index < folded.length; index++) positions.push({ start: offset, end: offset + character.length });
        normalized += folded;
        offset += character.length;
      }
      const needle = fold(term, accents);
      if (!needle) continue;
      let index = normalized.indexOf(needle);
      while (index !== -1) {
        hits.push({ start: positions[index].start, end: positions[index + needle.length - 1].end });
        index = normalized.indexOf(needle, index + 1);
      }
    }
    hits.sort((left, right) => left.start - right.start || left.end - right.end);
    const merged = [];
    for (const hit of hits) {
      const previous = merged.at(-1);
      if (previous && hit.start < previous.end) previous.end = Math.max(previous.end, hit.end);
      else merged.push({ ...hit });
    }
    for (const entry of nodes) {
      const matches = merged.filter(hit => hit.start < entry.end && hit.end > entry.start);
      for (const hit of matches.reverse()) {
        const range = document.createRange();
        range.setStart(entry.node, Math.max(0, hit.start - entry.start));
        range.setEnd(entry.node, Math.min(entry.end, hit.end) - entry.start);
        const mark = document.createElement('mark');
        mark.className = 'note-search-hit';
        range.surroundContents(mark);
      }
    }
  }
}

window.NoteEditor = { mountEditor, renderMarkdown, highlightSearch };