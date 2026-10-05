import { expect, test } from '@playwright/test';

test('register, upload, discover, like, and open a video', async ({ page }) => {
  const videos = [
    { id: 'video-ready', title: 'Release trailer', description: 'A demo video', status: 'READY', visibility: 'PUBLIC' },
  ];
  let liked = false;
  let uploaded = false;

  await page.route('http://localhost:8080/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname;

    if (pathname === '/auth/register' && request.method() === 'POST') {
      await route.fulfill({ status: 201, json: { user: { id: 'user-1' } } });
    } else if (pathname === '/videos' && request.method() === 'GET') {
      await route.fulfill({ json: uploaded ? [...videos, { id: 'video-uploaded', title: 'My first upload', description: '', status: 'PROCESSING', visibility: 'PRIVATE' }] : videos });
    } else if (pathname === '/videos' && request.method() === 'POST') {
      await route.fulfill({ status: 201, json: { id: 'video-uploaded', title: 'My first upload', description: '', status: 'UPLOADING', visibility: 'PRIVATE' } });
    } else if (pathname === '/videos/video-ready/likes' && request.method() === 'GET') {
      await route.fulfill({ json: { liked, count: liked ? 1 : 0 } });
    } else if (pathname === '/videos/video-ready/like' && request.method() === 'POST') {
      liked = !liked;
      await route.fulfill({ json: { liked, count: liked ? 1 : 0 } });
    } else if (pathname === '/search/videos' && request.method() === 'GET') {
      await route.fulfill({ json: videos });
    } else if (pathname === '/public/videos/video-ready/stream' && request.method() === 'GET') {
      await route.fulfill({ json: { playlist_url: 'http://localhost:8080/test-playlist.m3u8' } });
    } else if (pathname === '/videos/video-uploaded/upload/initiate' && request.method() === 'POST') {
      await route.fulfill({ json: { upload: { upload_url: 'http://localhost:8080/test-upload' } } });
    } else if (pathname === '/test-upload' && request.method() === 'PUT') {
      await route.fulfill({ status: 200 });
    } else if (pathname === '/videos/video-uploaded/upload/complete' && request.method() === 'POST') {
      uploaded = true;
      await route.fulfill({ status: 200, json: { status: 'queued' } });
    } else if (pathname === '/test-playlist.m3u8') {
      await route.fulfill({ status: 200, contentType: 'application/vnd.apple.mpegurl', body: '#EXTM3U\n#EXT-X-ENDLIST\n' });
    } else {
      await route.fulfill({ status: 404, json: { error: `Unexpected request: ${request.method()} ${pathname}` } });
    }
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Create account' }).click();
  await page.getByLabel('Email', { exact: true }).fill('creator@example.test');
  await page.getByLabel('Username', { exact: true }).fill('creator');
  await page.getByLabel('Password').fill('correct-horse-battery');
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'Your forge' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Release trailer' })).toBeVisible();

  await page.getByRole('button', { name: 'Like (0)' }).click();
  await expect(page.getByRole('button', { name: 'Unlike (1)' })).toBeVisible();

  await page.getByPlaceholder('Search videos').fill('release');
  await page.getByRole('button', { name: 'Search' }).click();
  await page.locator('.search-play').click();
  await expect(page.locator('#player-section')).toBeVisible();
  await expect(page.locator('#player')).toHaveAttribute('src', 'http://localhost:8080/test-playlist.m3u8');

  await page.getByLabel('Title').fill('My first upload');
  await page.getByLabel('Video file').setInputFiles({
    name: 'clip.mp4',
    mimeType: 'video/mp4',
    buffer: Buffer.from('test-video-content'),
  });
  await page.getByRole('button', { name: 'Upload video' }).click();
  await expect(page.locator('#upload-form .form-message')).toHaveText('Uploaded. Processing has started.');
  await expect(page.getByRole('heading', { name: 'My first upload' })).toBeVisible();
  await expect(page.getByText('PROCESSING', { exact: true })).toBeVisible();
});
