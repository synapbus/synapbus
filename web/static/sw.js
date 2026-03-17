const CACHE_NAME = 'synapbus-v1';
const STATIC_ASSETS = ['/', '/index.html'];

// Install: pre-cache app shell
self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll(STATIC_ASSETS))
  );
  self.skipWaiting();
});

// Activate: clean old caches
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((names) =>
      Promise.all(names.filter((n) => n !== CACHE_NAME).map((n) => caches.delete(n)))
    )
  );
  self.clients.claim();
});

// Fetch: network-only for API, cache-first for static
self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);

  // Network-only for API, auth, MCP, SSE
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/auth/') ||
      url.pathname.startsWith('/mcp') || url.pathname.startsWith('/a2a')) {
    return; // Let the browser handle normally
  }

  event.respondWith(
    caches.match(event.request).then((cached) => {
      if (cached) return cached;
      return fetch(event.request).then((response) => {
        // Only cache static assets (SvelteKit chunks, icons, fonts)
        if (response.ok && event.request.method === 'GET' &&
            (url.pathname.startsWith('/_app/') || url.pathname.startsWith('/icons/') ||
             url.pathname === '/' || url.pathname === '/index.html')) {
          const clone = response.clone();
          caches.open(CACHE_NAME).then(async (cache) => {
            cache.put(event.request, clone);
            // Evict oldest entries if cache exceeds 80 items
            const keys = await cache.keys();
            if (keys.length > 80) {
              for (let i = 0; i < keys.length - 80; i++) {
                cache.delete(keys[i]);
              }
            }
          });
        }
        return response;
      }).catch(() => {
        // Offline fallback for navigation requests
        if (event.request.mode === 'navigate') {
          return caches.match('/index.html');
        }
      });
    })
  );
});

// Push notification handling
self.addEventListener('push', (event) => {
  const data = event.data ? event.data.json() : {};
  const title = data.title || 'SynapBus';
  const options = {
    body: data.body || 'New notification',
    icon: '/icons/icon-192.png',
    badge: '/icons/icon-192.png',
    tag: data.tag || 'synapbus-notification',
    data: { url: data.url || '/' }
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

// Notification click: open the relevant page
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = event.notification.data?.url || '/';
  event.waitUntil(
    clients.matchAll({ type: 'window' }).then((clientList) => {
      for (const client of clientList) {
        if (client.url.includes(url) && 'focus' in client) return client.focus();
      }
      return clients.openWindow(url);
    })
  );
});
