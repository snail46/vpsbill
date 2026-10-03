import type { HTMLAttributes } from 'react'
import { createPortal } from 'react-dom'

// Backdrop is the full-screen layer under a modal, rendered at the end of
// <body>. Rendered in place, a card that moves on hover (a transform) would
// become the fixed layer's frame: the layer shrank to the card when the
// pointer left it, the card lost its hover, the layer filled the screen
// under the pointer again, and so on, many times a second.
export function Backdrop({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return createPortal(<div className={className ? `modal-backdrop ${className}` : 'modal-backdrop'} {...props} />, document.body)
}
