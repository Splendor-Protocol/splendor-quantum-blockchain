import { useEffect, useRef, useState } from 'react';

type CopyState = 'idle' | 'copied' | 'failed';

export function CodeBlock({ code, label = 'Copy' }: { code: string; label?: string }) {
  const [state, setState] = useState<CopyState>('idle');
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const copy = async () => {
    let ok = false;
    try {
      // The Clipboard API is missing on plain http and refuses without a user gesture.
      await navigator.clipboard.writeText(code);
      ok = true;
    } catch {
      ok = false;
    }
    setState(ok ? 'copied' : 'failed');
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setState('idle'), 2000);
  };

  return (
    <div className="pg-code">
      <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm pg-code-copy" onClick={() => void copy()}>
        {state === 'copied' ? 'Copied' : state === 'failed' ? 'Select and copy' : label}
      </button>
      <span className="pg-sr-only" role="status">
        {state === 'copied' ? 'Code copied to the clipboard' : state === 'failed' ? 'Copy failed. Select the code and copy it manually.' : ''}
      </span>
      <pre tabIndex={0}>
        <code>{code}</code>
      </pre>
    </div>
  );
}
