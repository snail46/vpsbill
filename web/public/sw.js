// Service worker: opens pages from the browser's last copy while the
// network catches up, so a repeat visit needs no round trip before it
// paints. Only page loads and the hashed bundles under /assets/ are
// handled; the API always goes to the network. Browsers run it only on
// HTTPS (or localhost).

const PAGES = 'vpsbill-pages-v1'
const ASSETS = 'vpsbill-assets-v1'

self.addEventListener('install', () => self.skipWaiting())

self.addEventListener('activate', event => {
  event.waitUntil(
    (async () => {
      for (const name of await caches.keys()) {
        if (name !== PAGES && name !== ASSETS) await caches.delete(name)
      }
      await self.clients.claim()
    })(),
  )
})

// Signing in or out: the stored pages carry who was signed in.
self.addEventListener('message', event => {
  if (event.data === 'forget-pages') event.waitUntil(caches.delete(PAGES))
})

self.addEventListener('fetch', event => {
  const request = event.request
  if (request.method !== 'GET') return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return
  // Whatever goes wrong here (a full or broken cache storage), the page
  // still loads from the network.
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(asset(request).catch(() => fetch(request)))
    return
  }
  if (request.mode === 'navigate' && !url.pathname.startsWith('/api/') && !url.pathname.startsWith('/health/')) {
    event.respondWith(page(event, url).catch(() => fetch(request)))
  }
})

// Bundles are named by their content, so a stored copy is always right.
async function asset(request) {
  const cache = await caches.open(ASSETS)
  const stored = await cache.match(request)
  if (stored) return stored
  const response = await fetch(request)
  if (response.ok) await cache.put(request, response.clone()).catch(() => undefined)
  return response
}

// The admin console and the customer portal get one stored page each:
// every address on a side serves the same index.html.
function pageKey(url) {
  return url.pathname === '/admin' || url.pathname.startsWith('/admin/') ? '/__page/admin' : '/__page/portal'
}

function page(event, url) {
  const key = pageKey(url)
  let saved = Promise.resolve()
  const network = fetch(event.request).then(response => {
    // A redirect is a proxy's login (Cloudflare Access and the like): the
    // stored page must not stand in for it next time.
    saved = (response.type === 'opaqueredirect' ? forget(key) : remember(key, response.clone())).catch(() => undefined)
    return response
  })
  // Keeping the fresh copy may outlast the response served now.
  event.waitUntil(network.then(() => saved).catch(() => undefined))
  return caches
    .open(PAGES)
    .then(cache => cache.match(key))
    .catch(() => undefined)
    .then(stored => (stored ? markStale(stored) : network))
}

// Only a page served for its own address is kept; redirects (the admin side
// answers / with one) and errors are not.
async function remember(key, response) {
  const type = response.headers.get('content-type') || ''
  if (!response.ok || response.type !== 'basic' || response.redirected || !type.includes('text/html')) return
  const cache = await caches.open(PAGES)
  const previous = await cache.match(key)
  await cache.put(key, response.clone())
  await pruneAssets(previous, response)
}

async function forget(key) {
  const cache = await caches.open(PAGES)
  await cache.delete(key)
}

// The stored page's boot data may be out of date (a sign-out elsewhere, an
// expired session); the page checks it again when it sees this mark.
async function markStale(response) {
  const html = await response.text()
  const marked = html.replace('<script id="vpsbill-boot" type="application/json">', '<script id="vpsbill-boot" type="application/json" data-stale="1">')
  const headers = new Headers(response.headers)
  headers.delete('content-length')
  headers.delete('content-encoding')
  return new Response(marked, { status: 200, headers })
}

function assetsOf(html) {
  return new Set(Array.from(html.matchAll(/\/?assets\/[\w.-]+\.(?:js|css)/g), match => '/' + match[0].replace(/^\//, '')))
}

// After a release the old bundles are dropped; ones the new page loads
// lazily are fetched again when needed.
async function pruneAssets(previous, current) {
  if (!previous) return
  const before = assetsOf(await previous.text())
  const after = assetsOf(await current.text())
  if (before.size === after.size && [...before].every(path => after.has(path))) return
  const cache = await caches.open(ASSETS)
  for (const request of await cache.keys()) {
    if (!after.has(new URL(request.url).pathname)) await cache.delete(request)
  }
}
