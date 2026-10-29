import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { useConfirm, type ConfirmOptions } from './ConfirmDialog';

afterEach(cleanup);

let ask: (o: ConfirmOptions) => Promise<boolean> = () => Promise.resolve(false);

function Host() {
  const [dialog, confirm] = useConfirm();
  ask = confirm;
  return <div>{dialog}</div>;
}

const options: ConfirmOptions = { title: 'Delete it?', message: 'Gone for good.', confirmLabel: 'Delete', danger: true };

describe('useConfirm', () => {
  it('resolves true on confirm and false on cancel, with Cancel focused first', async () => {
    render(<Host />);
    let answer: Promise<boolean>;
    act(() => {
      answer = ask(options);
    });
    expect(document.activeElement).toBe(screen.getByTestId('confirm-cancel'));
    fireEvent.click(screen.getByTestId('confirm-ok'));
    await expect(answer!).resolves.toBe(true);

    act(() => {
      answer = ask(options);
    });
    fireEvent.click(screen.getByTestId('confirm-cancel'));
    await expect(answer!).resolves.toBe(false);
  });

  it('answers "no" when its owner goes away with the question open', async () => {
    const { unmount } = render(<Host />);
    let answer: Promise<boolean>;
    act(() => {
      answer = ask(options);
    });
    unmount();
    await expect(answer!).resolves.toBe(false);
  });

  it('answers the first question "no" when a second one replaces it', async () => {
    render(<Host />);
    let first: Promise<boolean>;
    let second: Promise<boolean>;
    act(() => {
      first = ask(options);
    });
    act(() => {
      second = ask({ ...options, title: 'Really?' });
    });
    await expect(first!).resolves.toBe(false);
    fireEvent.click(screen.getByTestId('confirm-ok'));
    await expect(second!).resolves.toBe(true);
  });
});
