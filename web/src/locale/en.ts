// English: the interface text, and the text that comes from the server
// (messages and descriptions stored with the data).
import { ui } from './en-ui'
import { serverRules, serverWords } from './en-server'

export const words: Record<string, string> = { ...serverWords, ...ui }

export const rules: [RegExp, string][] = serverRules
