import { useEffect, useRef, useState } from 'react';

type CopyState = 'idle' | 'copied' | 'failed';

/** Copies `text` and says whether it worked, on the button and to assistive tech. */
export function CopyButton({
  text,
  label = 'Copy',
  done = 'Copied to the clipboard',
  className = 'pg-btn pg-btn-ghost pg-btn-sm',
  testId,
}: {
  text: string;
  label?: string;
  /** Announced after a successful copy. */
  done?: string;
  className?: string;
  testId?: string;
}) {
  const [state, setState] = useState<CopyState>('idle');
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const copy = async () => {
    let ok = false;
    try {
      // The Clipboard API is missing on plain http and refuses without a user gesture.
      await navigator.clipboard.writeText(text);
      ok = true;
    } catch {
      ok = false;
    }
    setState(ok ? 'copied' : 'failed');
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setState('idle'), 2000);
  };

  return (
    <>
      <button type="button" className={className} data-testid={testId} onClick={() => void copy()}>
        {state === 'copied' ? 'Copied' : state === 'failed' ? 'Select and copy' : label}
      </button>
      <span className="pg-sr-only" role="status">
        {state === 'copied' ? done : state === 'failed' ? 'Copy failed. Select the text and copy it manually.' : ''}
      </span>
    </>
  );
}

export function CodeBlock({ code, label = 'Copy' }: { code: string; label?: string }) {
  return (
    <div className="pg-code">
      <CopyButton text={code} label={label} done="Code copied to the clipboard" className="pg-btn pg-btn-ghost pg-btn-sm pg-code-copy" />
      <pre tabIndex={0}>
        <code>{code}</code>
      </pre>
    </div>
  );
}
