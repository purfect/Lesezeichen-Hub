import { test, expect } from '@playwright/test';

async function createBookmark(request) {
  const groupResponse = await request.post('/api/groups', {
    data: { name: `Termin-Test ${Date.now()}`, description: '', sort_order: 0 },
  });
  expect(groupResponse.ok()).toBeTruthy();
  const { id: groupId } = await groupResponse.json();
  const bookmarkResponse = await request.post('/api/bookmarks', {
    data: {
      group_id: groupId,
      title: 'Fälligkeit direkt entfernen',
      url: `https://example.com/reminder-${Date.now()}`,
      notes: '',
      tags: [],
      favorite: false,
      pinned: false,
      archived: false,
      sort_order: 0,
      remind_at: '2020-01-01',
    },
  });
  expect(bookmarkResponse.ok()).toBeTruthy();
  return (await bookmarkResponse.json()).id;
}

test('Removes a bookmark reminder directly from its dashboard entry', async ({ page, request }) => {
  const bookmarkId = await createBookmark(request);
  await page.goto('/');

  const bookmark = page.locator(`.bookmark-item[data-bookmark-id="${bookmarkId}"]`);
  await expect(bookmark.locator('.bookmark-reminder')).toContainText('Wiedervorlage überfällig');
  await bookmark.getByRole('button', { name: 'Fälligkeit entfernen' }).click();
  await expect(bookmark.locator('.bookmark-reminder')).toBeHidden();

  const stateResponse = await request.get('/api/state');
  const state = await stateResponse.json();
  const savedBookmark = state.groups.flatMap(group => group.bookmarks || []).find(item => item.id === bookmarkId);
  expect(savedBookmark).toBeTruthy();
  expect(savedBookmark?.remind_at).toBeFalsy();
});
