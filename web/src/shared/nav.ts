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

// osLabel turns a template id (an image alias or reference such as
// localhost/hatch-debian12:latest or ubuntu-2404-lxc) into a readable
// system name, falling back to the id itself.
export function osLabel(id: string) {
  if (!id) return '—'
  const name = (id.split('/').pop() ?? id).split(':')[0].toLowerCase()
  for (const [codename, label] of Object.entries(codenames)) if (name.includes(codename)) return label
  const rules: [RegExp, (match: RegExpMatchArray) => string][] = [
    [/ubuntu[-_ ]?(\d{2})\.?(\d{2})/, m => `Ubuntu ${m[1]}.${m[2]}`],
    [/debian[-_ ]?(\d{1,2})(?!\d)/, m => `Debian ${m[1]}`],
    [/alpine[-_ ]?(\d+\.\d+)?/, m => (m[1] ? `Alpine ${m[1]}` : 'Alpine')],
    [/(rocky|almalinux|centos|fedora|opensuse|arch|oracle)[-_ ]?(\d+(?:\.\d+)?)?/, m => {
      const names: Record<string, string> = { rocky: 'Rocky Linux', almalinux: 'AlmaLinux', centos: 'CentOS', fedora: 'Fedora', opensuse: 'openSUSE', arch: 'Arch Linux', oracle: 'Oracle Linux' }
      return m[2] ? `${names[m[1]]} ${m[2]}` : names[m[1]]
    }],
  ]
  for (const [pattern, format] of rules) {
    const match = name.match(pattern)
    if (match) return format(match)
  }
  return id
}

export const cycleLabels: Record<string, string> = { monthly: '月', quarterly: '季', semiannual: '半年', annual: '年' }
