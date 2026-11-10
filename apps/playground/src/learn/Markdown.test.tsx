import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Markdown } from './Markdown';

describe('Markdown', () => {
  it('renders markdown but strips script, handlers and javascript: links', () => {
    const { container } = render(
      <Markdown source={'# Title\n\n<script>window.x=1</script><img src="x" onerror="window.x=2">\n\n[link](javascript:alert(1))'} />,
    );
    expect(container.querySelector('h1')?.textContent).toBe('Title');
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('img')?.getAttribute('onerror')).toBeNull();
    expect(container.querySelector('a')?.getAttribute('href') ?? '').not.toMatch(/^javascript:/i);
  });

  it('code blocks and tables are keyboard-scrollable regions', () => {
    const { container } = render(<Markdown source={'```ts\nconst a = 1;\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |'} />);
    const pre = container.querySelector('pre')!;
    expect(pre.getAttribute('tabindex')).toBe('0');
    expect(pre.getAttribute('role')).toBe('region');
    expect(container.querySelector('.pg-table-wrap')?.getAttribute('tabindex')).toBe('0');
  });
});
