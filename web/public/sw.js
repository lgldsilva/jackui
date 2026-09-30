// JackUI service worker — Web Push only. There is deliberately NO fetch
// handler: caching an authenticated streaming app (Range requests, ?token=
// media URLs, HLS segments) behind a SW is a minefield, and installability
// already comes from the manifest.
self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { title: 'JackUI', body: event.data ? event.data.text() : '' }
  }
  event.waitUntil(self.registration.showNotification(data.title || 'JackUI', {
    body: data.body || '',
    icon: '/favicon.svg',
    badge: '/favicon.svg',
    data: { url: data.url || '/watchlist' },
  }))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  // data.url comes from the push payload (server-controlled but still external
  // input). Restrict navigation to same-origin URLs: `new URL(raw, origin)`
  // resolves relative paths ("/watchlist") against us, while absolute foreign
  // origins and dangerous schemes (javascript:, data: → origin "null") fail
  // the comparison. Anything else is ignored.
  const raw = event.notification.data?.url || '/'
  let target = null
  try {
    const parsed = new URL(raw, self.location.origin)
    if (parsed.origin === self.location.origin) target = parsed.href
  } catch {
    target = null
  }
  if (!target) return
  event.waitUntil((async () => {
    const list = await clients.matchAll({ type: 'window', includeUncontrolled: true })
    for (const client of list) {
      if ('focus' in client) {
        await client.focus()
        if ('navigate' in client) await client.navigate(target)
        return
      }
    }
    await clients.openWindow(target)
  })())
})
