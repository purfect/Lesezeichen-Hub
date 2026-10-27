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
  await expect(page.locator('#editor-content')).toBeHidden();
  await expect(rich.locator('h2')).toHaveText('Mein Ablauf');
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
  await page.getByRole('textbox', { name: 'Inhalt der Notiz' }).fill('Vertraulicher Inhalt');
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

for (const type of ['note', 'annotation', 'code', 'checklist']) {
  test(`Formatted editor and read view work for ${type}`, async ({ page, request }, testInfo) => {
    const source = '# Ablauf\n\n## Heute\n\n**Wichtig**\n\n- [x] Fertig\n- [ ] Offen\n\n```js\nconst value = "<tag>";\n```\n\nWeiter';
    const id = await createNote(request, source, { type });
    if (type === 'checklist') await page.setViewportSize({ width: 390, height: 1000 });
    await page.goto(`/static/notes.html?note_id=${id}`);
    await expect(page.locator('.note-markdown h1')).toHaveText('Ablauf');
    await expect(page.locator('.note-markdown h2')).toHaveText('Heute');
    await expect(page.locator('.note-markdown strong')).toHaveText('Wichtig');
    await page.screenshot({ path: testInfo.outputPath(`${type}-view.png`), fullPage: true });
    await page.locator('#btn-edit').click();
    const rich = page.getByRole('textbox', { name: 'Inhalt der Notiz' });
    await expect(page.locator('.note-format-toolbar')).toBeVisible();
    await expect(page.locator('#editor-type option[value="checklist"]')).toHaveCount(0);
    await expect(page.locator('#editor-type')).toHaveValue(type === 'checklist' ? 'note' : type);
    await expect(page.locator('#btn-add-step, #checklist-add-form')).toHaveCount(0);
    await expect(rich.locator('li[data-type="taskItem"]')).toHaveCount(2);
    await expect(rich.locator('input:checked')).toHaveCount(1);
    await page.locator('#editor-tags').fill('Metadaten');
    await expect(page.locator('#editor-autosave-status')).toContainText('Automatisch gespeichert');
    expect(await savedContent(request, id)).toBe(source);
    await rich.locator('p').last().click();
    await expect.poll(() => rich.evaluate(() => window.getSelection()?.anchorNode?.textContent)).toBe('Weiter');
    await page.getByRole('combobox', { name: 'Absatzformat' }).selectOption('3');
    await expect(rich.locator('h3')).toHaveText('Weiter');
    await page.screenshot({ path: testInfo.outputPath(`${type}-editor.png`), fullPage: true });
    await page.locator('#btn-cancel-edit').click();
    await expect(page.locator('.note-markdown h3')).toHaveText('Weiter');
    await expect(page.locator('.note-markdown pre')).toHaveText('const value = "<tag>";\n');
    const saved = await savedContent(request, id);
    expect(saved).toContain('- [x] Fertig');
    expect(saved).toContain('- [ ] Offen');
    expect(saved).toContain('### Weiter');
    {
      await expect(page.locator('.checklist-progress-label')).toContainText('1 von 2');
      await page.locator('.checklist-item input').nth(1).check();
      await expect(page.locator('.checklist-progress-label')).toContainText('2 von 2');
      await expect.poll(() => savedContent(request, id)).toContain('- [x] Offen');
      await page.locator('#btn-edit').click();
      await rich.locator('h3').click();
      await expect.poll(() => rich.evaluate(() => window.getSelection()?.anchorNode?.textContent)).toBe('Weiter');
      await rich.press('End');
      await rich.press('Enter');
      await expect.poll(() => rich.evaluate(() => window.getSelection()?.anchorNode?.textContent)).toBe('');
      await page.getByRole('button', { name: 'Aufgabenliste', exact: true }).click();
      await expect(rich).toBeFocused();
      await page.getByRole('textbox', { name: 'Inhalt der Notiz' }).pressSequentially('Neuer Schritt');
      await page.locator('#btn-cancel-edit').click();
      await expect(page.locator('.checklist-progress-label')).toContainText('2 von 3');
      await expect(page.locator('.checklist-item-text').last()).toContainText('Neuer Schritt');
    }
  });
}

test('Vault formats headings before and after encryption, reload and unlocking', async ({ page, request }) => {
  await page.goto('/static/notes.html?new=1');
  await page.locator('#editor-title').fill('Formatierter Vault');
  await page.locator('#editor-type').selectOption('vault');
  const rich = page.getByRole('textbox', { name: 'Inhalt der Notiz' });
  await rich.fill('Vertraulicher Abschnitt');
  await rich.press('ControlOrMeta+a');
  await page.getByRole('combobox', { name: 'Absatzformat' }).selectOption('2');
  await page.locator('#editor-vault-password').fill('Vault-Format-Test');
  await page.getByRole('button', { name: 'Erstellen', exact: true }).click();
  await expect(page.locator('.note-markdown h2')).toHaveText('Vertraulicher Abschnitt');
  const notes = await (await request.get('/api/notes')).json();
  const note = notes.notes.find(note => note.title === 'Formatierter Vault');
  expect(note.content).toMatch(/^vault:v1:/);
  expect(note.content).not.toContain('Vertraulicher Abschnitt');
  await page.goto(`/static/notes.html?note_id=${note.id}`);
  await expect(page.locator('.note-markdown')).toHaveCount(0);
  await page.locator('#vault-password').fill('Vault-Format-Test');
  await page.locator('#btn-vault-unlock').click();
  await expect(page.locator('.note-markdown h2')).toHaveText('Vertraulicher Abschnitt');
  page.on('dialog', dialog => dialog.accept('Vault-Format-Test'));
  await page.locator('#btn-edit').click();
  await expect(rich.locator('h2')).toHaveText('Vertraulicher Abschnitt');
  await rich.press('ControlOrMeta+a');
  await page.getByRole('button', { name: 'Fett', exact: true }).click();
  await page.locator('#editor-vault-password').fill('Vault-Format-Test');
  await page.getByRole('button', { name: 'Speichern', exact: true }).click();
  await expect(page.locator('.note-markdown h2 strong')).toHaveText('Vertraulicher Abschnitt');
  expect(await savedContent(request, note.id)).not.toBe(note.content);
  expect(await savedContent(request, note.id)).not.toContain('Vertraulicher Abschnitt');
});

test('Workflow counts and toggles only actual tasks, not code samples', async ({ page, request }) => {
  const source = '# Ablauf\n\n```\n- [ ] Nur ein Codebeispiel\n```\n\n{code}\n- [x] Ebenfalls Code\n{code}\n\n## Aufgaben\n\n- [ ] **Erster Schritt**\n  - [x] Unteraufgabe\n- [ ] Zweiter Schritt';
  const id = await createNote(request, source, { type: 'checklist' });
  await page.goto(`/static/notes.html?note_id=${id}`);
  await expect(page.locator('.checklist-progress-label')).toContainText('1 von 3');
  await expect(page.locator('.checklist-list h2')).toHaveText('Aufgaben');
  await expect(page.locator('.checklist-list strong')).toHaveText('Erster Schritt');
  await page.locator('.checklist-item input').last().check();
  await expect(page.locator('.checklist-progress-label')).toContainText('2 von 3');
  const saved = await savedContent(request, id);
  expect(saved).toContain('- [x] Zweiter Schritt');
  expect(saved).toContain('- [ ] Nur ein Codebeispiel');
  expect(saved).toContain('- [x] Ebenfalls Code');
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