import { t } from './i18n'
// navigatePortal moves the customer portal to a path such as
// /portal/services/<id>; the shell and the pages follow it through the
// popstate event, as they do for the browser's back button.
export function navigatePortal(path: string) {
  if (window.location.pathname + window.location.search + window.location.hash !== path) window.history.pushState(null, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

// portalPathPart returns a segment of the portal path: 1 is the page, 2 an
// item on it (e.g. a service id).
export function portalPathPart(index: number) {
  return window.location.pathname.split('/').filter(Boolean)[index] ?? ''
}

const codenames: Record<string, string> = {
  bookworm: 'Debian 12', bullseye: 'Debian 11', trixie: 'Debian 13', buster: 'Debian 10',
  noble: 'Ubuntu 24.04', jammy: 'Ubuntu 22.04', focal: 'Ubuntu 20.04',
}

// osOptions names the images a customer may pick by system and version
// only (osLabel), without the image references' prefixes. Two images that
// read the same are told apart by what their ids add (such as "cloud").
export function osOptions(ids: string[]) {
  const labels = ids.map(id => osLabel(id))
  return ids.map((id, index) => {
    const label = labels[index]
    if (labels.indexOf(label) === labels.lastIndexOf(label)) return { id, label }
    const extra = (id.split('/').pop() ?? id)
      .split(':')[0]
      .split(/[-_.]/)
      .filter(part => /^[a-z]{3,}$/i.test(part) && !label.toLowerCase().includes(part.toLowerCase()) && !['hatch', 'localhost', 'latest', 'lxc', 'amd64', 'all'].includes(part.toLowerCase()))
      .join(' ')
    const sameBefore = labels.slice(0, index).filter(other => other === label).length
    return { id, label: extra ? t('{0}（{1}）', label, extra) : t('{0}（{1}）', label, sameBefore + 1) }
  })
}

// osLabel turns a template id (an image alias or reference such as
// localhost/hatch-debian12:latest or ubuntu-2404-lxc) into a readable
// system name, falling back to the bare image name.
export function osLabel(id: string) {
  if (!id) return '—'
  // The whole reference without its tag: some name the system in the path
  // (images:rockylinux/9).
  const reference = id.toLowerCase().replace(/:[^/:]*$/, '')
  const name = reference.split('/').pop() || reference
  for (const [codename, label] of Object.entries(codenames)) if (reference.includes(codename)) return label
  const rules: [RegExp, (match: RegExpMatchArray) => string][] = [
    [/ubuntu[-_ ]?(\d{2})\.?(\d{2})/, m => `Ubuntu ${m[1]}.${m[2]}`],
    [/ubuntu[-_ ]?(\d{2})(?!\d)/, m => `Ubuntu ${m[1]}.04`],
    [/debian[-_ ]?(\d{1,2})(?!\d)/, m => `Debian ${m[1]}`],
    [/alpine[-_ ]?(\d+\.\d+)/, m => `Alpine ${m[1]}`],
    [/alpine[-_ ]?(\d)(\d{2})(?!\d)/, m => `Alpine ${m[1]}.${m[2]}`],
    [/alpine/, () => 'Alpine'],
    [/(rocky|almalinux|centos|fedora|opensuse|arch|oracle)(?:linux)?[-_ /]?(\d+(?:\.\d+)?)?/, m => {
      const names: Record<string, string> = { rocky: 'Rocky Linux', almalinux: 'AlmaLinux', centos: 'CentOS', fedora: 'Fedora', opensuse: 'openSUSE', arch: 'Arch Linux', oracle: 'Oracle Linux' }
      return m[2] ? `${names[m[1]]} ${m[2]}` : names[m[1]]
    }],
  ]
  for (const [pattern, format] of rules) {
    const match = reference.match(pattern)
    if (match) return format(match)
  }
  // Other systems: at least drop the registry path, the tag and the
  // image-name prefix.
  return name.replace(/^hatch-/, '') || id
}

