// Shared app state.
import { get, maybe, post } from './api.js';
import { toast } from './ui.js';

export const state = {
  me: null,
  sites: null, // cached list for the palette
  ai: undefined, // undefined = unknown, null = not installed, {configured}
  live: {}, // slug -> builder event hub handed over from Home
  fn: undefined, // server functions status (functions.js); null = not installed
};

export async function loadSites(force) {
  if (!state.sites || force) {
    const d = await get('/api/v1/sites', { quiet: true }).catch(() => ({ sites: [] }));
    state.sites = d.sites || [];
  }
  return state.sites;
}

export async function aiStatus() {
  if (state.ai === undefined) {
    const d = await maybe('/api/v1/ai/status').catch(() => null);
    state.ai = d ? { configured: d.configured ?? d.enabled ?? d.available ?? true, ...d } : null;
  }
  return state.ai;
}
export const aiReady = (s) => !!(s && s.configured);

export async function setStar(site, on) {
  site.starred = on;
  try {
    await post('/api/v1/sites/' + encodeURIComponent(site.slug) + '/star', { starred: on });
  } catch {
    site.starred = !on;
    return false;
  }
  if (state.sites) { const s = state.sites.find((x) => x.id === site.id); if (s) s.starred = on; }
  toast(on ? 'Starred. You’ll find it at the top of Home.' : 'Removed from starred', { timeout: 2400 });
  return true;
}

// Event hub so a stream started on one page can be watched from another.
export function hub() {
  return {
    events: [], subs: new Set(), done: false,
    push(t, d) { this.events.push([t, d]); if (t === 'done' || t === 'error') this.done = true; this.subs.forEach((f) => f(t, d)); },
    sub(f) { this.events.forEach(([t, d]) => f(t, d)); this.subs.add(f); return () => this.subs.delete(f); },
  };
}
