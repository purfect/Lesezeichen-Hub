import { test, expect } from '@playwright/test';

async function createNote(request, content, options = {}) {
  const response = await request.post('/api/notes', { data: { title: 'Formatprüfung', type: 'note', content, tags: [], bookmark_ids: [], links_clickable: true, ...options } });
  expect(response.ok()).toBeTruthy();
  return (await response.json()).id;
}

async function savedContent(request, id) {
  const response = await request.get(`/api/notes/${id}`);
  return (await response.json()).content;
}

test('Toolbar, selection, autosave, undo and type switching preserve the note', async ({ page, request }) => {
  const id = await createNote(request, 'Text für heute');
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(`/static/notes.html?note_id=${id}`);
  await page.locator('#btn-edit').click();
  const rich = page.getByRole('textbox', { name: 'Inhalt der Notiz' });
  await rich.fill('Mein Ablauf');
  await rich.press('ControlOrMeta+a');
  await page.getByRole('button', { name: 'Fett', exact: true }).click();
  await expect(rich.locator('strong')).toHaveText('Mein Ablauf');
  await expect.poll(() => savedContent(request, id)).toContain('**Mein Ablauf**');
  await page.getByRole('button', { name: 'Rückgängig', exact: true }).click();
  await expect(rich.locator('strong')).toHaveCount(0);
  await page.getByRole('button', { name: 'Wiederholen', exact: true }).click();
  await expect(rich.locator('strong')).toHaveText('Mein Ablauf');
  await page.getByRole('combobox', { name: 'Absatzformat' }).selectOption('2');
  await expect(rich.locator('h2')).toHaveText('Mein Ablauf');
  await page.locator('#editor-type').selectOption('code');
  await expect(page.locator('#editor-content')).toBeVisible();
  await expect(page.locator('#editor-content')).toHaveValue(/## \*\*Mein Ablauf\*\*/);
  await page.locator('#editor-type').selectOption('note');
  await expect(rich.locator('h2')).toHaveText('Mein Ablauf');
  await page.locator('#btn-cancel-edit').click();
  await expect(page.locator('.note-markdown h2')).toHaveText('Mein Ablauf');
  await expect.poll(() => savedContent(request, id)).toContain('## **Mein Ablauf**');
  expect(errors).toEqual([]);
});

test('Search highlights formatted text and clears without saving markup', async ({ page, request }) => {
  const source = '# GitHub\n\nGit**Hub** und Café.\n\n{code}\nGitHub\n{code}';
  const id = await createNote(request, source);
  await page.goto(`/static/notes.html?note_id=${id}&q=github%20cafe`);
  await expect(page.locator('.note-markdown mark')).toHaveCount(5);
  await expect(page.locator('#detail-title-label')).toHaveText('Formatprüfung');
  await page.locator('#notes-search-clear').click();
  await expect(page.locator('.note-markdown mark')).toHaveCount(0);
  expect(await savedContent(request, id)).toBe(source);
  await page.locator('#notes-search').fill('Hub');
  await expect(page.locator('.note-markdown mark')).toHaveCount(3);
  await page.locator('#btn-edit').click();
  const rich = page.getByRole('textbox', { name: 'Inhalt der Notiz' });
  await expect(rich.locator('mark')).toHaveCount(0);
  await page.locator('#editor-tags').fill('Neu');
  await expect(page.locator('#editor-autosave-status')).toContainText('Automatisch gespeichert');
  expect(await savedContent(request, id)).toBe(source);
});

test('Link dialog preserves selection and the clickable preference', async ({ page, request }) => {
  const id = await createNote(request, 'Ziel');
  await page.goto(`/static/notes.html?note_id=${id}`);
  await page.locator('#btn-edit').click();
  const rich = page.getByRole('textbox', { name: 'Inhalt der Notiz' });
  await rich.click();
  await rich.press('ControlOrMeta+a');
  await page.getByRole('button', { name: 'Link', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('textbox', { name: 'URL', exact: true }).fill('javascript:alert(1)');
  await page.getByRole('button', { name: 'Übernehmen' }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page.getByRole('alert')).toContainText('HTTP');
  await page.getByRole('textbox', { name: 'URL', exact: true }).fill('https://example.com/');
  await page.getByRole('button', { name: 'Übernehmen' }).click();
  await expect(rich.locator('a')).toHaveText('Ziel');
  await page.locator('#editor-links-clickable').uncheck();
  await page.locator('#btn-cancel-edit').click();
  await expect(page.locator('.note-markdown')).toHaveText('Ziel');
  await expect(page.locator('.note-markdown a')).toHaveCount(0);
  expect(await savedContent(request, id)).toContain('[Ziel](https://example.com/)');
});

test('Failed autosave keeps the editor open, empty content can be saved', async ({ page, request }) => {
  const id = await createNote(request, 'Vorher');
  await page.goto(`/static/notes.html?note_id=${id}`);
  await page.locator('#btn-edit').click();
  await page.route(`**/api/notes/${id}`, route => route.request().method() === 'PUT' ? route.fulfill({ status: 500, body: 'Testfehler' }) : route.continue());
  const rich = page.getByRole('textbox', { name: 'Inhalt der Notiz' });
  await rich.fill('Nicht verlieren');
  await page.locator('#btn-cancel-edit').click();
  await expect(rich).toBeVisible();
  await expect(page.locator('#editor-autosave-status')).toHaveClass(/is-error/);
  expect(await savedContent(request, id)).toBe('Vorher');
  await page.unroute(`**/api/notes/${id}`);
  await rich.fill('');
  await page.locator('#btn-cancel-edit').click();
  await expect(rich).toHaveCount(0);
  expect(await savedContent(request, id)).toBe('');
});

test('New notes, lists and safe rendering work without external runtime requests', async ({ page, request }) => {
  const external = [];
  page.on('request', request => { if (!request.url().startsWith('http://127.0.0.1:18769/')) external.push(request.url()); });
  await page.goto('/static/notes.html?new=1');
  await page.locator('#editor-title').fill('Neue Formatnotiz');
  const rich = page.getByRole('textbox', { name: 'Inhalt der Notiz' });
  await rich.fill('Erster Schritt');
  await page.getByRole('button', { name: 'Aufzählung', exact: true }).click();
  await rich.press('End');
  await rich.press('Enter');
  await rich.pressSequentially('Zweiter Schritt');
  await expect(rich.locator('li')).toHaveCount(2);
  await page.getByRole('button', { name: 'Erstellen', exact: true }).click();
  await expect(page.locator('.note-markdown li')).toHaveCount(2);
  await page.locator('#btn-new-note').click();
  await expect(page.getByRole('textbox', { name: 'Inhalt der Notiz' })).toBeVisible();
  const id = await createNote(request, '<script>window.injected=true</script>\n\n[Nein](javascript:alert%281%29)\n\n<img src=x onerror=alert(1)>');
  await page.goto(`/static/notes.html?note_id=${id}`);
  await expect(page.locator('.note-markdown script, .note-markdown img, .note-markdown a')).toHaveCount(0);
  expect(await page.evaluate(() => window.injected)).toBeUndefined();
  expect(external).toEqual([]);
});

test('Global search passes its query to the note view', async ({ page, request }) => {
  const id = await createNote(request, '# GlobaleNotizSuche\n\nTreffer', { title: 'GlobaleNotizSuche' });
  await page.goto('/');
  const search = page.locator('#search');
  await search.fill('GlobaleNotizSuche');
  await page.locator('#search-notes').check();
  const link = page.locator(`#note-hits-list a[href*="note_id=${id}"]`);
  await expect(link).toHaveAttribute('href', /q=GlobaleNotizSuche/i);
  await link.click();
  await expect(page.locator('.note-markdown mark')).toHaveText('GlobaleNotizSuche');
});

test('Edits during an in-flight autosave survive immediate closing', async ({ page, request }) => {
  const id = await createNote(request, 'Vorher');
  await page.goto(`/static/notes.html?note_id=${id}`);
  await page.locator('#btn-edit').click();
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  let started;
  const saving = new Promise(resolve => { started = resolve; });
  await page.route(`**/api/notes/${id}`, async route => {
    if (route.request().method() === 'PUT') {
      started();
      await pending;
    }
    await route.continue();
  });
  const rich = page.getByRole('textbox', { name: 'Inhalt der Notiz' });
  await rich.fill('Erste Fassung');
  await saving;
  await rich.fill('Aktuellste Fassung');
  const closing = page.locator('#btn-cancel-edit').click();
  release();
  await closing;
  await expect(page.locator('.note-markdown')).toHaveText('Aktuellste Fassung');
  expect(await savedContent(request, id)).toBe('Aktuellste Fassung');
});

test('Manual saving temporarily locks the form and keeps the original unedited source', async ({ page, request }) => {
  const source = '{code}\noriginal\n{code}';
  const id = await createNote(request, source);
  await page.goto(`/static/notes.html?note_id=${id}`);
  await page.locator('#btn-edit').click();
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  let started;
  const saving = new Promise(resolve => { started = resolve; });
  await page.route(`**/api/notes/${id}`, async route => {
    if (route.request().method() === 'PUT') {
      started();
      await pending;
    }
    await route.continue();
  });
  await page.getByRole('button', { name: 'Speichern', exact: true }).click();
  await saving;
  await expect(page.locator('#note-editor-form')).toHaveAttribute('inert', '');
  await expect(page.locator('.note-rich-content')).toHaveAttribute('contenteditable', 'false');
  release();
  await expect(page.locator('.note-markdown pre')).toHaveText('original\n');
  expect(await savedContent(request, id)).toBe(source);
});

test('Cleared search ignores a late response and checklist controls remain interactive', async ({ page, request }) => {
  const id = await createNote(request, '# Heute\n- [ ] GitHub prüfen', { type: 'checklist' });
  await page.goto(`/static/notes.html?note_id=${id}`);
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  let started;
  const searching = new Promise(resolve => { started = resolve; });
  await page.route('**/api/notes?q=GitHub', async route => {
    const response = await route.fetch();
    started();
    await pending;
    await route.fulfill({ response });
  });
  await page.locator('#notes-search').fill('GitHub');
  await searching;
  await page.locator('#notes-search-clear').click();
  release();
  await page.unrouteAll({ behavior: 'wait' });
  await expect(page.locator('.checklist-list mark')).toHaveCount(0);
  await page.locator('#notes-search').fill('GitHub');
  await expect(page.locator('.checklist-item-text mark')).toHaveText('GitHub');
  await page.locator('.checklist-item input').check();
  await expect(page.locator('.checklist-item input')).toBeChecked();
  await expect.poll(() => savedContent(request, id)).toContain('- [x]');
});

test('Vault conversion never autosaves plaintext and requires explicit saving', async ({ page, request }) => {
  page.on('dialog', dialog => dialog.accept(dialog.type() === 'prompt' ? 'Nur-ein-Testpasswort' : undefined));
  await page.goto('/static/notes.html?new=1');
  await page.locator('#editor-title').fill('Geschützter Entwurf');
  await page.locator('#editor-type').selectOption('vault');
  await page.locator('#editor-content').fill('Vertraulicher Inhalt');
  await page.locator('#editor-vault-password').fill('Nur-ein-Testpasswort');
  await page.getByRole('button', { name: 'Erstellen', exact: true }).click();
  await expect(page.locator('#btn-edit')).toBeVisible();
  const notes = await (await request.get('/api/notes')).json();
  const note = notes.notes.find(note => note.title === 'Geschützter Entwurf');
  expect(note.type).toBe('vault');
  expect(note.content).not.toContain('Vertraulicher Inhalt');
  await page.locator('#btn-edit').click();
  await page.locator('#editor-type').selectOption('note');
  await expect(page.getByRole('textbox', { name: 'Inhalt der Notiz' })).toHaveText('Vertraulicher Inhalt');
  await expect(page.locator('#editor-autosave-status')).toContainText('bitte mit Speichern');
  expect(await savedContent(request, note.id)).toBe(note.content);
  await page.getByRole('button', { name: 'Speichern', exact: true }).click();
  await expect(page.locator('.note-markdown')).toHaveText('Vertraulicher Inhalt');
  expect(await savedContent(request, note.id)).toBe('Vertraulicher Inhalt');
});

for (const theme of ['modern', 'terminal']) {
  for (const width of [1440, 390, 360]) {
    test(`Layout ${theme} ${width}px`, async ({ page, request }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.addInitScript(value => localStorage.setItem('lsz_theme', value), theme);
      const id = await createNote(request, '# Projektplan\n\nEine **übersichtliche** Notiz mit [Quelle](https://example.com).\n\n## Heute\n\n- Aufgabe vorbereiten\n- Ergebnis prüfen\n\n> Wichtig für morgen\n\n```\nLanger_Code_ohne_Umbruch_' + 'x'.repeat(100) + '\n```');
      await page.goto(`/static/notes.html?note_id=${id}&q=Projektplan`);
      await expect(page.locator('.note-markdown h1')).toBeVisible();
      for (const button of await page.locator('.notes-detail-actions button').all()) {
        const bounds = await button.boundingBox();
        expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ path: testInfo.outputPath('note-view.png'), fullPage: true });
      await page.locator('#btn-edit').click();
      await expect(page.locator('.note-format-toolbar')).toBeVisible();
      const toolbar = await page.locator('.note-format-toolbar').boundingBox();
      expect(toolbar.x + toolbar.width).toBeLessThanOrEqual(width);
      for (const button of await page.locator('.note-format-toolbar button, .note-format-toolbar select').all()) {
        const bounds = await button.boundingBox();
        expect(bounds.x).toBeGreaterThanOrEqual(toolbar.x);
        expect(bounds.x + bounds.width).toBeLessThanOrEqual(toolbar.x + toolbar.width + 1);
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ path: testInfo.outputPath('note-editor.png'), fullPage: true });
      await page.getByRole('button', { name: 'Link', exact: true }).click();
      await expect(page.getByRole('dialog')).toBeVisible();
      const dialog = await page.getByRole('dialog').boundingBox();
      expect(dialog.x).toBeGreaterThanOrEqual(0);
      expect(dialog.x + dialog.width).toBeLessThanOrEqual(width);
    });
  }
}