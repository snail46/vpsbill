import { defineConfig, type Plugin, type Rolldown } from 'vite'
import react from '@vitejs/plugin-react'
type OutputBundle = Rolldown.OutputBundle
type OutputChunk = Rolldown.OutputChunk

// preloadSurface makes index.html start downloading the admin console or
// the customer portal (whichever the address opens) together with the main
// script. Both are loaded lazily (see App.tsx), so without this the browser
// would only ask for them after the main script ran: one more round trip
// before anything shows.
function preloadSurface(): Plugin {
  return {
    name: 'vpsbill-preload-surface',
    apply: 'build',
    transformIndexHtml: {
      order: 'post',
      handler(_html, context) {
        const bundle = context.bundle
        if (!bundle) return
        const surfaces = {
          admin: chunkFiles(bundle, 'src/admin/AdminApp.tsx'),
          portal: chunkFiles(bundle, 'src/portal/PortalApp.tsx'),
        }
        const script =
          `(function(){var s=${JSON.stringify(surfaces)}[location.pathname.indexOf('/admin')===0?'admin':'portal'];` +
          `s.js.forEach(function(f){var l=document.createElement('link');l.rel='modulepreload';l.href='/'+f;document.head.appendChild(l)});` +
          `s.css.forEach(function(f){var l=document.createElement('link');l.rel='stylesheet';l.href='/'+f;document.head.appendChild(l)})})()`
        // First in <head>: an inline script after a stylesheet waits for it.
        return [{ tag: 'script', children: script, injectTo: 'head-prepend' }]
      },
    },
  }
}

// chunkFiles lists the lazy chunk built from entry and the chunks it
// imports, leaving out the main entry that index.html loads anyway.
function chunkFiles(bundle: OutputBundle, entry: string) {
  const chunks = Object.values(bundle).filter((item): item is OutputChunk => item.type === 'chunk')
  const root = chunks.find(chunk => chunk.facadeModuleId?.replace(/\\/g, '/').endsWith(entry))
  const js = new Set<string>()
  const css = new Set<string>()
  const visit = (chunk: OutputChunk | undefined) => {
    if (!chunk || chunk.isEntry || js.has(chunk.fileName)) return
    js.add(chunk.fileName)
    chunk.viteMetadata?.importedCss.forEach(file => css.add(file))
    chunk.imports.forEach(name => visit(chunks.find(item => item.fileName === name)))
  }
  visit(root)
  if (!root) throw new Error(`preloadSurface: no chunk for ${entry}`)
  return { js: [...js], css: [...css] }
}

export default defineConfig({
  plugins: [react(), preloadSurface()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
    },
  },
})
