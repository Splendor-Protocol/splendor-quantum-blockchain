import { useMemo } from 'react';
import DOMPurify from 'dompurify';
import { marked } from 'marked';

/**
 * Renders repo docs bundled at build time — never user input. Still sanitized: marked passes raw
 * HTML through, and a docs change should not be able to put script on the playground's origin.
 */
export function Markdown({ source }: { source: string }) {
  const html = useMemo(() => DOMPurify.sanitize(marked.parse(source, { async: false, gfm: true }) as string), [source]);
  return <article className="pg-markdown" dangerouslySetInnerHTML={{ __html: html }} />;
}
