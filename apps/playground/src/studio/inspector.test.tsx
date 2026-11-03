import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { WIDGET_MANIFESTS } from '@splendor/connect-react/manifest';
import { BLOCK_MANIFESTS, createCatalog, emptySpec, isContainer, type ContainerNode } from '@splendor/dapp-spec';
import { PropsForm, numberFieldError, numberRangeHint } from '../components/PropsForm';
import { paletteMatches } from './Palette';
import { CommandPalette, matchCommand } from './CommandPalette';
import { styleFieldError } from './Inspector';
import { insertBlocker, insertionPoint } from '../builder/state';

const catalog = createCatalog(WIDGET_MANIFESTS);
const entry = (id: string) => catalog.find((e) => e.id === id)!;

afterEach(() => cleanup());

describe('PropsForm fields', () => {
  it('long text props get a text area', () => {
    render(<PropsForm entry={entry('hero')} values={{ heading: 'Hi', text: 'Line one\nLine two' }} onChange={() => undefined} />);
    const area = document.getElementById('prop-hero-text') as HTMLTextAreaElement;
    expect(area.tagName).toBe('TEXTAREA');
    expect(area.value).toBe('Line one\nLine two');
    expect((document.getElementById('prop-hero-heading') as HTMLInputElement).tagName).toBe('INPUT');
    expect(BLOCK_MANIFESTS.find((b) => b.id === 'members-area')!.props.find((p) => p.name === 'content')!.multiline).toBe(true);
  });

  it('number props show the range and default, use whole steps and flag values outside it', () => {
    const onChange = vi.fn();
    render(<PropsForm entry={entry('validator-list')} values={{ maxRows: 500 }} onChange={onChange} />);
    const input = document.getElementById('prop-validator-list-maxRows') as HTMLInputElement;
    expect(input.step).toBe('1');
    expect(screen.getByText('Between 1 and 200')).toBeTruthy();
    expect(screen.getByText('At most 200')).toBeTruthy();
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect((document.getElementById('prop-validator-list-pollMs') as HTMLInputElement).placeholder).toBe('30000');
    expect(numberFieldError({ min: 1 }, 0)).toBe('At least 1');
    expect(numberFieldError({ min: 1, max: 5 }, 3)).toBeNull();
    expect(numberRangeHint({})).toBeNull();
  });

  it('select props name their default', () => {
    render(<PropsForm entry={entry('qr-code')} values={{ value: 'x' }} onChange={() => undefined} />);
    expect(screen.getByRole('option', { name: 'Default (Medium (15%))' })).toBeTruthy();
  });

  it('address props can be filled from suggestions', () => {
    const onChange = vi.fn();
    const me = 'SQ:' + 'ab'.repeat(32);
    render(
      <PropsForm
        entry={entry('tip-jar')}
        values={{}}
        onChange={onChange}
        addressSuggestions={[{ label: 'Test wallet (preview)', address: me }]}
      />,
    );
    fireEvent.change(screen.getByTestId('prop-tip-jar-recipient-fill'), { target: { value: me } });
    expect(onChange).toHaveBeenCalledWith('recipient', me);
  });
});

describe('command palette', () => {
  it('matches every typed word against label, group and keywords, and runs the active command', () => {
    const run = vi.fn();
    const cmds = [
      { id: 'a', label: 'Add Hero text', group: 'Add', keywords: 'component layout', run },
      { id: 'b', label: 'Export or deploy…', group: 'Project', keywords: 'vercel', run: vi.fn() },
    ];
    expect(matchCommand(cmds[0]!, 'add layout')).toBe(true);
    expect(matchCommand(cmds[1]!, 'vercel')).toBe(true);
    expect(matchCommand(cmds[1]!, 'hero')).toBe(false);
    const onClose = vi.fn();
    render(<CommandPalette commands={cmds} onClose={onClose} />);
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'hero' } });
    expect(screen.getAllByRole('option')).toHaveLength(1);
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onClose).toHaveBeenCalled();
    expect(run).toHaveBeenCalled();
  });
});

describe('style and insertion helpers', () => {
  it('style errors explain what is accepted', () => {
    expect(styleFieldError('50%')).toBeNull();
    expect(styleFieldError('')).toBeNull();
    expect(styleFieldError('url(x)')).toMatch(/plain CSS value/);
  });

  it('inserts after the selected component, inside a selected box, else at the end', () => {
    const spec = emptySpec('x');
    const root = spec.pages[0]!.root as ContainerNode;
    const stack = root.children[0] as ContainerNode;
    stack.children = [
      { kind: 'block', block: { id: 'a', widget: 'hero', props: {} } },
      { kind: 'block', block: { id: 'b', widget: 'hero', props: {} } },
    ];
    expect(insertionPoint(root, 'a')).toEqual({ parentId: stack.id, index: 1 });
    expect(insertionPoint(root, stack.id)).toEqual({ parentId: stack.id, index: 2 });
    expect(insertionPoint(root, null)).toEqual({ parentId: root.id, index: root.children.length });
    expect(isContainer(root)).toBe(true);
    expect(insertBlocker(spec.pages[0]!, stack.id)).toBeNull();
  });

  it('palette search matches the shelf name, id and import name', () => {
    expect(paletteMatches(entry('nft-gallery'), 'collectibles')).toBe(true);
    expect(paletteMatches(entry('x402-paywall'), 'X402Paywall')).toBe(true);
    expect(paletteMatches(entry('validator-list'), 'chain')).toBe(true);
    expect(paletteMatches(entry('hero'), 'qrc-721')).toBe(false);
  });
});
