import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.setOptions({ breaks: true, gfm: true })

// Links the operator opens in a new tab must not get window.opener.
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A' && node.getAttribute('target')) node.setAttribute('rel', 'noopener noreferrer')
})

/**
 * Any admin can edit the landing markdown (Settings, Branding). Unsanitised,
 * marked renders <script> and <img onerror=...> straight into every visitor's
 * DOM: admin-to-visitor XSS. Every render goes through DOMPurify before it
 * reaches dangerouslySetInnerHTML.
 */
export function renderSafeMarkdown(md: string): string {
  const raw = marked.parse(md, { async: false }) as string
  return DOMPurify.sanitize(raw, { USE_PROFILES: { html: true } })
}

/** Typography for rendered markdown, scoped to the container so it cannot leak. */
export const MARKDOWN_CLASS = [
  'text-[14px] leading-relaxed text-muted',
  '[&_h1]:mb-2 [&_h1]:mt-5 [&_h1]:text-lg [&_h1]:font-semibold [&_h1]:text-ink',
  '[&_h2]:mb-2 [&_h2]:mt-5 [&_h2]:text-base [&_h2]:font-semibold [&_h2]:text-ink',
  '[&_h3]:mb-1.5 [&_h3]:mt-4 [&_h3]:text-[14px] [&_h3]:font-semibold [&_h3]:text-ink',
  '[&_p]:mb-3 [&_ul]:mb-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:mb-3 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:mb-1',
  '[&_a]:text-accent [&_a]:underline [&_a]:underline-offset-2 hover:[&_a]:brightness-110',
  '[&_strong]:font-semibold [&_strong]:text-ink',
  '[&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-raised [&_code]:px-1 [&_code]:font-mono [&_code]:text-xs',
  '[&_pre]:mb-3 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:border [&_pre]:border-line [&_pre]:bg-bg [&_pre]:p-3',
  '[&_blockquote]:mb-3 [&_blockquote]:border-l-2 [&_blockquote]:border-line-strong [&_blockquote]:pl-3',
  '[&_img]:max-w-full [&_img]:rounded-md [&_hr]:my-4 [&_hr]:border-line',
  '[&>*:last-child]:mb-0 [&>*:first-child]:mt-0',
].join(' ')
