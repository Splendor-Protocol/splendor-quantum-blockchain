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
});
