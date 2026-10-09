import { useMemo } from 'react';
import { marked } from 'marked';

/** Renders repo docs (trusted, bundled at build time — never user input). */
export function Markdown({ source }: { source: string }) {
  const html = useMemo(() => marked.parse(source, { async: false, gfm: true }) as string, [source]);
  return <article className="pg-markdown" dangerouslySetInnerHTML={{ __html: html }} />;
}
