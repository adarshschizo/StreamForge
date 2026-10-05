import './style.css';

const API = import.meta.env.VITE_API_URL ?? 'http://localhost:8080';
const app = document.querySelector<HTMLDivElement>('#app');
if (!app) throw new Error('StreamForge web root is missing');

type Video = { id: string; title: string; description: string; status: string; visibility: string };
let loadedVideos: Video[] = [];
type ProcessingStatus = { status: string; progress: number; stage: string; error?: string };
type MultipartDraft = {
  videoId: string;
  uploadId: string;
  title: string;
  fileName: string;
  fileSize: number;
  lastModified: number;
  completedParts: number[];
};

const PART_SIZE = 8 * 1024 * 1024;
const draftKey = 'streamforge.multipart.draft';

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`${API}${path}`, {
    ...options,
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(options.headers ?? {}) },
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(body.error ?? 'Request failed');
  }
  return response.status === 204 ? (undefined as T) : response.json();
}

function renderAuth(message = '') {
  app.innerHTML = `<main class="shell auth-shell"><section class="hero"><p class="eyebrow">StreamForge</p><h1>Upload once.<br><em>Stream everywhere.</em></h1><p class="lede">Forge every upload into adaptive HLS video.</p></section><section class="card auth-card"><div class="tabs"><button class="tab active" data-mode="login">Log in</button><button class="tab" data-mode="register">Create account</button></div><form id="auth-form"><label class="login-only">Email or username<input name="login" type="text" autocomplete="username" required></label><label class="register-only hidden">Email<input name="email" type="email"></label><label class="register-only hidden">Username<input name="username" type="text"></label><label>Password<input name="password" type="password" minlength="8" required></label><button class="primary" type="submit">Continue</button><p class="form-message">${message}</p></form></section></main>`;
  let mode = 'login';
  const syncMode = () => {
    document.querySelectorAll<HTMLElement>('.register-only').forEach((el) => el.classList.toggle('hidden', mode === 'login'));
    document.querySelectorAll<HTMLElement>('.login-only').forEach((el) => el.classList.toggle('hidden', mode !== 'login'));
    document.querySelectorAll<HTMLButtonElement>('.tab').forEach((tab) => tab.classList.toggle('active', tab.dataset.mode === mode));
    document.querySelector<HTMLInputElement>('[name="login"]')!.required = mode === 'login';
    document.querySelector<HTMLInputElement>('[name="email"]')!.required = mode === 'register';
    document.querySelector<HTMLInputElement>('[name="username"]')!.required = mode === 'register';
  };
  document.querySelectorAll<HTMLButtonElement>('.tab').forEach((tab) => tab.onclick = () => { mode = tab.dataset.mode ?? 'login'; syncMode(); });
  document.querySelector<HTMLFormElement>('#auth-form')!.onsubmit = async (event) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget as HTMLFormElement);
    const payload = mode === 'login' ? { login: form.get('login'), password: form.get('password') } : { email: form.get('email'), username: form.get('username'), password: form.get('password') };
    try {
      await request(mode === 'login' ? '/auth/login' : '/auth/register', { method: 'POST', body: JSON.stringify(payload) });
      renderDashboard();
    } catch (error) {
      renderAuth(error instanceof Error ? error.message : 'Unable to authenticate');
    }
  };
  syncMode();
}

function renderDashboard(message = '') {
  app.innerHTML = `<main class="shell dashboard"><header class="topbar"><div><p class="eyebrow">StreamForge</p><h2>Your forge</h2></div><button id="logout" class="ghost">Log out</button></header><section class="card upload-card"><div><p class="eyebrow">New upload</p><h3>Turn a file into a stream</h3><p class="helper">Large files upload in resumable 8 MB parts. You can safely retry after a network interruption.</p></div><form id="upload-form"><div class="form-grid"><label>Title<input name="title" required maxlength="200" placeholder="Launch video"></label><label>Visibility<select name="visibility"><option value="PRIVATE">Private</option><option value="UNLISTED">Unlisted</option><option value="PUBLIC">Public</option></select></label><label>Video file<input name="file" type="file" accept="video/*" required></label></div><button class="primary" type="submit">Upload video</button><progress id="upload-progress" class="hidden" max="100" value="0"></progress><p id="upload-progress-label" class="progress-label"></p><p class="form-message">${message}</p></form></section><section id="player-section" class="card player-card hidden"><div class="section-heading"><div><p class="eyebrow">Playback</p><h3 id="player-title">Now playing</h3></div><button id="close-player" class="ghost">Close</button></div><video id="player" controls playsinline></video><p id="player-message" class="form-message"></p></section><section><div class="section-heading"><div><p class="eyebrow">Discover</p><h3>Public library</h3></div><form id="search-form" class="search-form"><input name="q" placeholder="Search videos"><button class="ghost" type="submit">Search</button></form></div><div id="search-list" class="video-list"></div></section><section><div class="section-heading"><div><p class="eyebrow">Library</p><h3>Your videos</h3></div><button id="refresh" class="ghost">Refresh</button></div><div id="video-list" class="video-list"></div></section></main>`;
  document.querySelector<HTMLButtonElement>('#logout')!.onclick = async () => { await request('/auth/logout', { method: 'POST' }); renderAuth(); };
  document.querySelector<HTMLButtonElement>('#refresh')!.onclick = () => loadVideos();
  document.querySelector<HTMLFormElement>('#search-form')!.onsubmit = async (event) => {
    event.preventDefault();
    const query = encodeURIComponent(String(new FormData(event.currentTarget as HTMLFormElement).get('q') ?? ''));
    const results = await request<Video[]>(`/search/videos?${query}`);
    const list = document.querySelector<HTMLDivElement>('#search-list');
    loadedVideos = [...loadedVideos, ...results.filter((result) => !loadedVideos.some((video) => video.id === result.id))];
    if (list) list.innerHTML = results.length ? results.map((video) => `<article class="video-row"><div><h4>${escapeHTML(video.title)}</h4><p>${escapeHTML(video.description || 'Public video')}</p></div><span class="badge ready">READY</span><button class="ghost search-play" data-id="${video.id}">Play</button><button class="ghost like" data-id="${video.id}">Like</button></article>`).join('') : '<div class="empty">No public videos found.</div>';
    list?.querySelectorAll<HTMLButtonElement>('.search-play').forEach((button) => button.onclick = () => playVideo(button.dataset.id ?? ''));
    list?.querySelectorAll<HTMLButtonElement>('.like').forEach((button) => button.onclick = () => toggleLike(button));
  };
  document.querySelector<HTMLButtonElement>('#close-player')!.onclick = () => {
    const player = document.querySelector<HTMLVideoElement>('#player');
    if (player) { player.pause(); player.removeAttribute('src'); player.load(); }
    document.querySelector('#player-section')?.classList.add('hidden');
  };
  document.querySelector<HTMLFormElement>('#upload-form')!.onsubmit = uploadVideo;
  loadVideos();
  window.setInterval(() => loadVideos(), 5000);
}

async function loadVideos() {
  const list = document.querySelector<HTMLDivElement>('#video-list');
  if (!list) return;
  try {
    const videos = await request<Video[]>('/videos');
    loadedVideos = videos;
    const statuses = await Promise.all(videos.map(async (video) => {
      if (video.status === 'READY' || video.status === 'FAILED') return [video.id, null] as const;
      try { return [video.id, await request<ProcessingStatus>(`/videos/${video.id}/processing`)] as const; } catch { return [video.id, null] as const; }
    }));
    const progressByVideo = new Map(statuses);
    const likes = await Promise.all(videos.map(async (video) => {
      if (video.status !== 'READY') return [video.id, { liked: false, count: 0 }] as const;
      try { return [video.id, await request<{ liked: boolean; count: number }>(`/videos/${video.id}/likes`)] as const; } catch { return [video.id, { liked: false, count: 0 }] as const; }
    }));
    const likesByVideo = new Map(likes);
    list.innerHTML = videos.length ? videos.map((video) => {
      const progress = progressByVideo.get(video.id);
      const displayStatus = progress?.status ?? video.status;
      const progressText = progress && displayStatus !== 'READY' ? `<small>${escapeHTML(progress.stage)} · ${progress.progress}%</small>` : '';
      const like = likesByVideo.get(video.id);
      return `<article class="video-row"><div><h4>${escapeHTML(video.title)}</h4><p>${escapeHTML(video.description || 'No description')}</p>${progressText}</div><span class="badge ${displayStatus.toLowerCase()}">${escapeHTML(displayStatus)}</span>${displayStatus === 'READY' ? `<button class="ghost play" data-id="${video.id}">Play</button><button class="ghost like${like?.liked ? ' active' : ''}" data-id="${video.id}">${like?.liked ? 'Unlike' : 'Like'} (${like?.count ?? 0})</button>` : ''}</article>`;
    }).join('') : '<div class="empty">Your library is ready for its first upload.</div>';
    list.querySelectorAll<HTMLButtonElement>('.play').forEach((button) => button.onclick = async () => {
      await playVideo(button.dataset.id ?? '');
    });
    list.querySelectorAll<HTMLButtonElement>('.like').forEach((button) => button.onclick = () => toggleLike(button));
  } catch (error) {
    list.innerHTML = `<div class="empty error">${error instanceof Error ? error.message : 'Unable to load videos'}</div>`;
  }

  async function toggleLike(button: HTMLButtonElement) {
    try {
      const summary = await request<{ liked: boolean; count: number }>(`/videos/${button.dataset.id}/like`, { method: 'POST' });
      button.textContent = `${summary.liked ? 'Unlike' : 'Like'} (${summary.count})`;
      button.classList.toggle('active', summary.liked);
    } catch (error) {
      button.title = error instanceof Error ? error.message : 'Unable to update like';
    }
  }
}

async function playVideo(videoID: string) {
  try {
      const video = loadedVideos.find((item) => item.id === videoID);
      const path = video?.visibility === 'PRIVATE' ? `/videos/${videoID}/stream` : `/public/videos/${videoID}/stream`;
      const result = await request<{ playlist_url: string }>(path);
      const player = document.querySelector<HTMLVideoElement>('#player');
      const section = document.querySelector('#player-section');
      const title = document.querySelector<HTMLElement>('#player-title');
      const status = document.querySelector<HTMLElement>('#player-message');
      if (!player || !section) return;
      if (title) title.textContent = video?.title ?? 'Now playing';
      player.src = result.playlist_url;
      section.classList.remove('hidden');
      if (status) status.textContent = 'If playback does not start, open the playlist URL in a browser with HLS support.';
      await player.play().catch(() => undefined);
  } catch (error) {
    const status = document.querySelector<HTMLElement>('#player-message');
    if (status) status.textContent = error instanceof Error ? error.message : 'Unable to play video';
  }
}

async function uploadVideo(event: SubmitEvent) {
  event.preventDefault();
  const formElement = event.currentTarget as HTMLFormElement;
  const form = new FormData(formElement);
  const file = form.get('file');
  const message = document.querySelector<HTMLParagraphElement>('#upload-form .form-message');
  try {
    if (!(file instanceof File) || file.size === 0) throw new Error('Choose a video file');
    const title = String(form.get('title') ?? '');
    const progress = document.querySelector<HTMLProgressElement>('#upload-progress');
    const progressLabel = document.querySelector<HTMLParagraphElement>('#upload-progress-label');
    if (message) message.textContent = 'Creating upload...';
    if (file.size >= PART_SIZE) {
      await uploadMultipart(file, title, progress, progressLabel, message);
    } else {
      const video = await request<Video>('/videos', { method: 'POST', body: JSON.stringify({ title, visibility: form.get('visibility') }) });
      const initiated = await request<{ upload: { upload_url: string } }>(`/videos/${video.id}/upload/initiate`, { method: 'POST' });
      if (message) message.textContent = 'Uploading video...';
      const upload = await fetch(initiated.upload.upload_url, { method: 'PUT', body: file });
      if (!upload.ok) throw new Error('Storage upload failed');
      await request(`/videos/${video.id}/upload/complete`, { method: 'POST' });
    }
    if (message) message.textContent = 'Uploaded. Processing has started.';
    formElement.reset();
    await loadVideos();
  } catch (error) {
    if (message) message.textContent = error instanceof Error ? error.message : 'Upload failed';
  }

  async function uploadMultipart(file: File, title: string, progress: HTMLProgressElement | null, progressLabel: HTMLParagraphElement | null, message: HTMLParagraphElement | null) {
    const totalParts = Math.ceil(file.size / PART_SIZE);
    let draft = readDraft(file, title);
    if (!draft) {
      const video = await request<Video>('/videos', { method: 'POST', body: JSON.stringify({ title, visibility: form.get('visibility') }) });
      const initiated = await request<{ upload: { upload_id: string } }>(`/videos/${video.id}/upload/multipart/initiate`, { method: 'POST' });
      draft = { videoId: video.id, uploadId: initiated.upload.upload_id, title, fileName: file.name, fileSize: file.size, lastModified: file.lastModified, completedParts: [] };
      saveDraft(draft);
    } else if (message) {
      message.textContent = `Resuming ${draft.fileName}...`;
    }
    if (progress) progress.classList.remove('hidden');
    for (let part = 1; part <= totalParts; part += 1) {
      if (draft.completedParts.includes(part)) {
        updateProgress(progress, progressLabel, draft.completedParts.length, totalParts);
        continue;
      }
      const start = (part - 1) * PART_SIZE;
      await retryPart(file.slice(start, Math.min(start + PART_SIZE, file.size)), draft.videoId, draft.uploadId, part);
      draft.completedParts.push(part);
      saveDraft(draft);
      updateProgress(progress, progressLabel, draft.completedParts.length, totalParts);
    }
    await request(`/videos/${draft.videoId}/upload/multipart/${draft.uploadId}/complete`, {
      method: 'POST',
      body: JSON.stringify({ total_parts: totalParts }),
    });
    localStorage.removeItem(draftKey);
  }

  async function retryPart(part: Blob, videoId: string, uploadId: string, partNumber: number) {
    let lastError: Error | undefined;
    for (let attempt = 1; attempt <= 3; attempt += 1) {
      try {
        const response = await fetch(`${API}/videos/${videoId}/upload/multipart/${uploadId}/parts/${partNumber}`, {
          method: 'PUT',
          body: part,
          credentials: 'include',
        });
        if (!response.ok) throw new Error(`Part ${partNumber} failed (${response.status})`);
        return;
      } catch (error) {
        lastError = error instanceof Error ? error : new Error('Part upload failed');
        if (attempt < 3) await new Promise((resolve) => setTimeout(resolve, 500 * attempt));
      }
    }
    throw lastError ?? new Error(`Part ${partNumber} failed`);
  }

  function readDraft(file: File, title: string): MultipartDraft | null {
    try {
      const draft = JSON.parse(localStorage.getItem(draftKey) ?? 'null') as MultipartDraft | null;
      return draft && draft.fileName === file.name && draft.fileSize === file.size && draft.lastModified === file.lastModified && draft.title === title ? draft : null;
    } catch {
      return null;
    }
  }

  function saveDraft(draft: MultipartDraft) {
    localStorage.setItem(draftKey, JSON.stringify(draft));
  }

  function updateProgress(progress: HTMLProgressElement | null, label: HTMLParagraphElement | null, completed: number, total: number) {
    const percent = Math.round((completed / total) * 100);
    if (progress) progress.value = percent;
    if (label) label.textContent = `${percent}% uploaded (${completed}/${total} parts)`;
  }
}

function escapeHTML(value: string) { return value.replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#039;' }[character] ?? character)); }
renderAuth();
