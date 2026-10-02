// Checks the English dictionary against the code: every text passed to
// t() needs an entry in src/locale/en-ui.ts, and an entry may only use the
// placeholders ({0}, {1}…) its Chinese text has. Run by "npm run build".
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

const root = new URL('../src/', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')

function walk(dir, found = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) walk(full, found)
    else if (/\.tsx?$/.test(entry.name)) found.push(full)
  }
  return found
}

// decode reads the inside of a single-quoted string literal.
const decode = text => text.replace(/\\(.)/g, (_, c) => (c === 'n' ? '\n' : c))
const literal = "'((?:[^'\\\\]|\\\\.)*)'"

const dictionary = new Map()
const entry = new RegExp(`^  ${literal}: ${literal},$`)
for (const line of readFileSync(join(root, 'locale/en-ui.ts'), 'utf8').split(/\r?\n/)) {
  const match = entry.exec(line)
  if (match) dictionary.set(decode(match[1]), decode(match[2]))
}

const used = new Map()
const call = new RegExp(`(?<![\\w.$])t\\(\\s*${literal}`, 'g')
for (const file of walk(root)) {
  if (file.includes('locale')) continue
  const source = readFileSync(file, 'utf8')
  for (const match of source.matchAll(call)) {
    const key = decode(match[1])
    if (!used.has(key)) used.set(key, file.slice(root.length))
  }
}

const placeholders = text => new Set(text.match(/\{\d+\}/g) ?? [])
const problems = []
for (const [key, file] of used) {
  if (!dictionary.has(key)) {
    problems.push(`not translated (${file}): ${key}`)
    continue
  }
  const allowed = placeholders(key)
  for (const mark of placeholders(dictionary.get(key))) {
    if (!allowed.has(mark)) problems.push(`${mark} is not in the original (${file}): ${key}`)
  }
}
const unused = [...dictionary.keys()].filter(key => !used.has(key))

if (problems.length) {
  console.error(problems.join('\n'))
  console.error(`\n${problems.length} problem(s) in src/locale/en-ui.ts; see docs/I18N.md`)
  process.exit(1)
}
console.log(`i18n: ${used.size} texts translated${unused.length ? `, ${unused.length} entries no longer used` : ''}`)
