// On phones, tables inside .table-wrap are shown as one card per row (see
// styles.css). Each cell needs its column name for that, so this copies the
// header text into data-label on every cell, for all tables, as they render.

function labelTable(table: HTMLTableElement) {
  const headers = Array.from(table.tHead?.rows[0]?.cells ?? []).map(cell => cell.textContent?.trim() ?? '')
  if (!headers.length) return
  for (const body of Array.from(table.tBodies)) {
    for (const row of Array.from(body.rows)) {
      let column = 0
      for (const cell of Array.from(row.cells)) {
        const label = cell.colSpan > 1 ? '' : headers[column] ?? ''
        if (cell.dataset.label !== label) cell.dataset.label = label
        column += cell.colSpan
      }
    }
  }
}

export function initTableLabels() {
  let queued = false
  const run = () => {
    queued = false
    document.querySelectorAll<HTMLTableElement>('.table-wrap table').forEach(labelTable)
  }
  new MutationObserver(() => {
    if (!queued) {
      queued = true
      requestAnimationFrame(run)
    }
  }).observe(document.body, { childList: true, subtree: true })
  run()
}
