// Site-wide 公告 (admin-authored markdown). Visible only to logged-in users; pops
// up whenever the current user hasn't seen the latest version — at login AND on
// any normal visit. An admin edit changes the version (content hash) so it
// re-pops for everyone who hasn't seen the new text.
import { reactive } from 'vue'
import { api, jsonBody } from './api'

export const announcement = reactive({ content: '', version: '', show: false })

// Fetch the current announcement + this user's seen-state; show it if unseen.
export async function loadAnnouncement() {
  try {
    const r = await api('/announcement')
    if (r.ok && r.data) {
      announcement.content = r.data.content || ''
      announcement.version = r.data.version || ''
      announcement.show = !r.data.seen && !!announcement.content.trim()
    }
  } catch { /* offline — skip */ }
}

// Manual open (公告 button in the left rail): re-fetch so the text is fresh
// even if nothing was loaded yet, then show regardless of seen-state.
export async function openAnnouncement() {
  try {
    const r = await api('/announcement')
    if (r.ok && r.data) {
      announcement.content = r.data.content || ''
      announcement.version = r.data.version || ''
    }
  } catch { /* offline — show whatever we have */ }
  announcement.show = true
}

// Dismiss: hide + remember this version server-side so it won't re-pop.
export async function dismissAnnouncement() {
  announcement.show = false
  if (announcement.version) {
    try { await api('/announcement/seen', jsonBody('POST', { version: announcement.version })) } catch { /* ignore */ }
  }
}
