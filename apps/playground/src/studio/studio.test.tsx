import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TEMPLATES, instantiateTemplate, type DappSpec } from '@splendor/dapp-spec';
import { ContractsPanel, describeTry } from './ContractsPanel';
import { FlowsPanel, collectLinks } from './FlowsPanel';
import { ONBOARDING_FORCE_KEY, ONBOARDING_KEY, Onboarding, shouldShowOnboarding } from './Onboarding';
import { PagesPanel } from './PagesPanel';
import { initBuilder } from '../builder/state';

const launchpad = (): DappSpec => initBuilder(instantiateTemplate(TEMPLATES.find((t) => t.id === 'token-launchpad')!)).spec;

/** Launchpad plus a button on `/` that links to `/manage`. */
function withLink(): DappSpec {
  const spec = launchpad();
  const home = spec.pages[0]!;
  const block = { id: 'go', widget: 'action-button', props: { label: 'Manage tokens', href: '/manage' } };
  home.blocks = [...(home.blocks ?? []), block];
  const stack = (home.root as { children: { children: unknown[] }[] }).children[0]!;
  stack.children.push({ kind: 'block', block });
  return spec;
}

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe('PagesPanel: duplicate and set home', () => {
  it('dispatches duplicatePage and setHomePage; the home page has no set-home button', () => {
    const dispatch = vi.fn();
    render(<PagesPanel spec={launchpad()} pagePath="/" dispatch={dispatch} newPageTitle={null} setNewPageTitle={() => undefined} />);
    fireEvent.click(screen.getByRole('button', { name: 'Duplicate page Manage' }));
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'duplicatePage', path: '/manage' });
    fireEvent.click(screen.getByRole('button', { name: 'Set Manage as home page' }));
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'setHomePage', path: '/manage' });
    expect(screen.queryByRole('button', { name: /Set Launch as home page/ })).toBeNull();
    expect(screen.getByTestId('page-is-home')).toBeTruthy();
  });
});

describe('FlowsPanel', () => {
  it('collects internal links only, with the source block id', () => {
    const links = collectLinks(withLink());
    expect(links).toEqual([{ fromPath: '/', fromTitle: 'Launch', fromBlockId: 'go', to: '/manage', label: 'Manage tokens' }]);
    const spec = withLink();
    spec.pages[0]!.blocks!.find((b) => b.id === 'go')!.props.href = 'https://example.com';
    expect(collectLinks(spec)).toEqual([]);
  });

  it('selects the source block when an edge or list entry is clicked', () => {
    const onSelectBlock = vi.fn();
    render(<FlowsPanel spec={withLink()} onSelectBlock={onSelectBlock} />);
    fireEvent.click(screen.getByTestId('flow-edge'));
    expect(onSelectBlock).toHaveBeenLastCalledWith('/', 'go');
    fireEvent.keyDown(screen.getByTestId('flow-edge'), { key: 'Enter' });
    expect(onSelectBlock).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByTestId('flow-link'));
    expect(onSelectBlock).toHaveBeenCalledTimes(3);
  });
});

describe('ContractsPanel Try', () => {
  const spec: DappSpec = {
    ...launchpad(),
    contracts: [{ id: 'counter', name: 'Counter', address: 'SQ:' + 'ab'.repeat(32), functions: [{ name: 'get', signature: 'get()', params: [], kind: 'read' }] }],
  };

  it('describes the call without sending anything', () => {
    expect(describeTry({ name: 'Counter', address: 'SQ:1' }, { signature: 'get()', kind: 'read' }, 'mock')).toBe('Would call get() on SQ:1 in mock mode');
    expect(describeTry({ name: 'C', address: ' ' }, { signature: 'inc()', kind: 'write' }, 'live')).toBe('Would call inc() on (no address yet) in live mode');
  });

  it('opens a dialog naming the signature, address and mode', () => {
    render(<ContractsPanel spec={spec} dispatch={() => undefined} onInsertContractBlock={() => undefined} mode="live" />);
    fireEvent.click(screen.getByTestId('contract-try-counter-get'));
    expect(screen.getByTestId('contract-try-result').textContent).toContain('Would call get() on SQ:');
    expect(screen.getByTestId('contract-try-result').textContent).toContain('in live mode');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('contract-try-dialog')).toBeNull();
  });
});

describe('Onboarding', () => {
  it('walks three steps and remembers that it was finished', () => {
    const onRailChange = vi.fn();
    render(<Onboarding onRailChange={onRailChange} />);
    expect(screen.getByTestId('onboarding-step').textContent).toBe('Step 1 of 3');
    expect(onRailChange).toHaveBeenCalledWith('components');
    fireEvent.click(screen.getByTestId('onboarding-next'));
    expect(screen.getByTestId('onboarding-step').textContent).toBe('Step 2 of 3');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByTestId('onboarding-step').textContent).toBe('Step 1 of 3');
    fireEvent.click(screen.getByTestId('onboarding-next'));
    fireEvent.click(screen.getByTestId('onboarding-next'));
    expect(screen.getByTestId('onboarding-next').textContent).toBe('Done');
    fireEvent.click(screen.getByTestId('onboarding-next'));
    expect(screen.queryByTestId('onboarding')).toBeNull();
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe('1');
  });

  it('skip also sets the flag, and a set flag keeps it closed', () => {
    const { unmount } = render(<Onboarding />);
    fireEvent.click(screen.getByTestId('onboarding-skip'));
    expect(localStorage.getItem(ONBOARDING_KEY)).toBe('1');
    unmount();
    render(<Onboarding />);
    expect(screen.queryByTestId('onboarding')).toBeNull();
  });

  it('stays out of the way of browser automation unless forced', () => {
    Object.defineProperty(navigator, 'webdriver', { value: true, configurable: true });
    try {
      expect(shouldShowOnboarding()).toBe(false);
      localStorage.setItem(ONBOARDING_FORCE_KEY, '1');
      expect(shouldShowOnboarding()).toBe(true);
    } finally {
      Object.defineProperty(navigator, 'webdriver', { value: false, configurable: true });
    }
  });
});
