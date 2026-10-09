import { useState } from 'react';

export function CodeBlock({ code, label = 'Copy' }: { code: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="pg-code">
      <button
        type="button"
        className="pg-btn pg-btn-ghost pg-code-copy"
        onClick={() => {
          void navigator.clipboard?.writeText(code).then(() => {
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1500);
          });
        }}
      >
        {copied ? 'Copied' : label}
      </button>
      <pre>
        <code>{code}</code>
      </pre>
    </div>
  );
}
