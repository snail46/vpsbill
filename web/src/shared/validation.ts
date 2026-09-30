// Form checks without the browser's own validation bubbles: when a submit
// finds an invalid field, the field is outlined and a short message appears
// under it until it is edited. Forms keep their required/min/max/pattern
// attributes; only the presentation changes.

type Field = HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement

function message(field: Field): string {
  const validity = field.validity
  const input = field instanceof HTMLInputElement ? field : null
  if (validity.valueMissing) {
    if (input?.type === 'checkbox') return '请勾选此项'
    if (input?.type === 'file') return '请选择文件'
    return field instanceof HTMLSelectElement ? '请选择一项' : '请填写此项'
  }
  if (validity.badInput) return '请输入有效的数字'
  if (validity.typeMismatch) {
    if (input?.type === 'email') return '请输入有效的邮箱地址'
    if (input?.type === 'url') return '请输入有效的网址，例如 https://example.com'
    return '格式不正确'
  }
  if (validity.rangeUnderflow && input) return `不能小于 ${input.min}`
  if (validity.rangeOverflow && input) return `不能大于 ${input.max}`
  if (validity.stepMismatch && input) return input.step === '1' || input.step === '' ? '请输入整数' : `请按 ${input.step} 的间隔填写`
  if (validity.tooShort && input) return `至少 ${input.minLength} 个字符`
  if (validity.tooLong && input) return `最多 ${input.maxLength} 个字符`
  if (validity.patternMismatch) return field.title || '格式不正确'
  return field.validationMessage || '内容无效'
}

function hintFor(field: Field): HTMLElement {
  const next = field.nextElementSibling
  if (next instanceof HTMLElement && next.classList.contains('field-invalid-hint')) return next
  const hint = document.createElement('small')
  hint.className = 'field-invalid-hint'
  hint.setAttribute('role', 'alert')
  field.insertAdjacentElement('afterend', hint)
  return hint
}

function clear(field: Field) {
  field.classList.remove('field-invalid')
  field.removeAttribute('aria-invalid')
  const next = field.nextElementSibling
  if (next instanceof HTMLElement && next.classList.contains('field-invalid-hint')) next.remove()
}

let focusedAt = 0

export function initFormValidation() {
  // "invalid" does not bubble; listen in the capture phase.
  document.addEventListener(
    'invalid',
    event => {
      const field = event.target
      if (!(field instanceof HTMLInputElement || field instanceof HTMLSelectElement || field instanceof HTMLTextAreaElement)) return
      event.preventDefault()
      field.classList.add('field-invalid')
      field.setAttribute('aria-invalid', 'true')
      hintFor(field).textContent = message(field)
      // One submit reports every invalid field; bring the first into view.
      if (Date.now() - focusedAt > 300) {
        focusedAt = Date.now()
        field.focus({ preventScroll: true })
        field.scrollIntoView({ block: 'center', behavior: 'smooth' })
      }
    },
    true,
  )
  const recheck = (event: Event) => {
    const field = event.target
    if ((field instanceof HTMLInputElement || field instanceof HTMLSelectElement || field instanceof HTMLTextAreaElement) && field.classList.contains('field-invalid')) {
      if (field.validity.valid) clear(field)
      else hintFor(field).textContent = message(field)
    }
  }
  document.addEventListener('input', recheck, true)
  document.addEventListener('change', recheck, true)
}
