import { expect } from 'vitest';
import { fireEvent, waitFor, type within } from '@testing-library/react';

/** Review screens keep Confirm disabled while the fee estimate loads; wait for it, then click. */
export async function clickWhenEnabled(root: ReturnType<typeof within>, name: RegExp): Promise<void> {
  const button = await waitFor(() => {
    const b = root.getByRole('button', { name }) as HTMLButtonElement;
    expect(b.disabled).toBe(false);
    return b;
  });
  fireEvent.click(button);
}
