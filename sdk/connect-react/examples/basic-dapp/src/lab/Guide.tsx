import { useEffect, useMemo, useState, type ReactNode } from 'react';
import guideSource from '../../../../docs/GUIDE.md?raw';
import { useLab } from './ui';

export function slugify(heading: string): string {
  return heading
    .replace(/^\d+\.\s*/, '')
    .toLowerCase()
    .replace(/`/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
}

type Block =
  | { kind: 'heading'; level: number; text: string; slug: string }
  | { kind: 'code'; lang: string; code: string }
  | { kind: 'table'; head: string[]; rows: string[][] }
  | { kind: 'list'; items: string[] }
  | { kind: 'para'; text: string };

function splitRow(line: string): string[] {
  const cells: string[] = [];
  let cur = '';
  let inCode = false;
  const body = line.trim().replace(/^\|/, '').replace(/\|$/, '');
  for (const ch of body) {
    if (ch === '`') inCode = !inCode;
    if (ch === '|' && !inCode) {
      cells.push(cur.trim());
      cur = '';
    } else cur += ch;
  }
  cells.push(cur.trim());
  return cells;
}

const isTableRule = (l: string | undefined) => !!l && /^\|?\s*:?-{3,}/.test(l.trim());
const isListItem = (l: string) => /^\s*[-*]\s+/.test(l);

function parse(md: string): Block[] {
  const lines = md.replace(/\r\n/g, '\n').split('\n');
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    if (!line.trim()) {
      i++;
      continue;
    }
    const fence = line.match(/^```(\w*)/);
    if (fence) {
      const code: string[] = [];
      i++;
      while (i < lines.length && !lines[i]!.startsWith('```')) code.push(lines[i++]!);
      i++;
      out.push({ kind: 'code', lang: fence[1] ?? '', code: code.join('\n') });
      continue;
    }
    const h = line.match(/^(#{1,6})\s+(.*)$/);
    if (h) {
      out.push({ kind: 'heading', level: h[1]!.length, text: h[2]!, slug: slugify(h[2]!) });
      i++;
      continue;
    }
    if (line.trim().startsWith('|') && isTableRule(lines[i + 1])) {
      const head = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i]!.trim().startsWith('|')) rows.push(splitRow(lines[i++]!));
      out.push({ kind: 'table', head, rows });
      continue;
    }
    if (isListItem(line)) {
      const items: string[] = [];
      while (i < lines.length && lines[i]!.trim()) {
        const l = lines[i]!;
        if (isListItem(l)) items.push(l.replace(/^\s*[-*]\s+/, ''));
        else items[items.length - 1] += ' ' + l.trim();
        i++;
      }
      out.push({ kind: 'list', items });
      continue;
    }
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i]!.trim() &&
      !/^(```|#{1,6}\s)/.test(lines[i]!) &&
      !isListItem(lines[i]!) &&
      !(lines[i]!.trim().startsWith('|') && isTableRule(lines[i + 1]))
    ) {
      para.push(lines[i++]!.trim());
    }
    out.push({ kind: 'para', text: para.join(' ') });
  }
  return out;
}

function Inline({ text, onAnchor }: { text: string; onAnchor: (slug: string) => void }) {
  const parts: ReactNode[] = [];
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let k = 0;
  while ((m = re.exec(text))) {
    if (m.index > last) parts.push(text.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith('`')) parts.push(<code key={k++}>{tok.slice(1, -1)}</code>);
    else if (tok.startsWith('**')) {
      parts.push(
        <strong key={k++}>
          <Inline text={tok.slice(2, -2)} onAnchor={onAnchor} />
        </strong>,
      );
    } else {
      const [, label, href] = tok.match(/^\[([^\]]+)\]\(([^)]+)\)$/)!;
      if (/^https?:\/\//.test(href!)) {
        parts.push(
          <a key={k++} href={href} target="_blank" rel="noreferrer">
            {label}
          </a>,
        );
      } else if (href!.startsWith('#')) {
        parts.push(
          <a
            key={k++}
            href={href}
            onClick={(e) => {
              e.preventDefault();
              onAnchor(href!.slice(1));
            }}
          >
            {label}
          </a>,
        );
      } else {
        const file = href!.replace(/^(\.\.\/)+/, '');
        parts.push(
          <span key={k++} className="lab-doc-ref" title={file}>
            {label} <small>({file})</small>
          </span>,
        );
      }
    }
    last = m.index + tok.length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return <>{parts}</>;
}

function CodeBlock({ code, lang }: { code: string; lang: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard?.writeText(code).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };
  return (
    <div className="lab-doc-code">
      <div className="lab-doc-code__bar">
        <span>{lang || 'text'}</span>
        <button type="button" className="lab-link" onClick={copy}>
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre>
        <code>{code}</code>
      </pre>
    </div>
  );
}

export function GuideView() {
  const { guideSection, openTests } = useLab();
  const blocks = useMemo(() => parse(guideSource), []);
  const sections = useMemo(
    () => blocks.filter((b): b is Extract<Block, { kind: 'heading' }> => b.kind === 'heading' && b.level === 2),
    [blocks],
  );

  const scrollTo = (slug: string) => {
    document.getElementById(`guide-${slug}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  useEffect(() => {
    if (guideSection) requestAnimationFrame(() => scrollTo(guideSection));
    else window.scrollTo({ top: 0 });
  }, [guideSection]);

  return (
    <div className="lab-layout">
      <nav className="lab-nav" aria-label="Guide sections">
        <button type="button" className="lab-link lab-doc-back" onClick={openTests}>
          Back to the tests
        </button>
        {sections.map((s) => (
          <a
            key={s.slug}
            href={`#guide-${s.slug}`}
            onClick={(e) => {
              e.preventDefault();
              scrollTo(s.slug);
            }}
          >
            {s.text.replace(/`/g, '')}
          </a>
        ))}
      </nav>
      <article className="lab-card lab-doc">
        {blocks.map((b, idx) => {
          switch (b.kind) {
            case 'heading': {
              const Tag = `h${Math.min(b.level, 4)}` as 'h1' | 'h2' | 'h3' | 'h4';
              return (
                <Tag key={idx} id={`guide-${b.slug}`}>
                  <Inline text={b.text} onAnchor={scrollTo} />
                </Tag>
              );
            }
            case 'code':
              return <CodeBlock key={idx} code={b.code} lang={b.lang} />;
            case 'table':
              return (
                <div key={idx} className="lab-doc-table">
                  <table>
                    <thead>
                      <tr>
                        {b.head.map((c, j) => (
                          <th key={j}>
                            <Inline text={c} onAnchor={scrollTo} />
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {b.rows.map((r, j) => (
                        <tr key={j}>
                          {r.map((c, n) => (
                            <td key={n}>
                              <Inline text={c} onAnchor={scrollTo} />
                            </td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              );
            case 'list':
              return (
                <ul key={idx}>
                  {b.items.map((it, j) => (
                    <li key={j}>
                      <Inline text={it} onAnchor={scrollTo} />
                    </li>
                  ))}
                </ul>
              );
            default:
              return (
                <p key={idx}>
                  <Inline text={b.text} onAnchor={scrollTo} />
                </p>
              );
          }
        })}
      </article>
    </div>
  );
}