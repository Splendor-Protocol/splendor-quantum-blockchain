import { useEffect, useRef, useState } from 'react';
import { copyText, truncateAddress } from '../utils.js';
import { COMMON_LABELS, useWidgetLabels, type CommonWidgetLabels } from '../labels.js';

type CopyLabels = Partial<Pick<CommonWidgetLabels, 'copy' | 'copied'>>;

export type CopyButtonProps = {
  text: string;
  labels?: CopyLabels;
  onCopied?: (text: string) => void;
};

/** Small "Copy" button that flips to "Copied" for a moment. */
export function CopyButton({ text, labels, onCopied }: CopyButtonProps) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  const onClick = async () => {
    if (!(await copyText(text))) return;
    setCopied(true);
    onCopied?.(text);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1400);
  };
  return (
    <button type="button" className="sqc-copy" onClick={() => void onClick()}>
      {copied ? l.copied : l.copy}
    </button>
  );
}

export type AddressDisplayProps = {
  /** Address or hash to show. */
  value: string;
  /** Explorer link for this value; omitted when null. */
  href?: string | null;
  /** Characters kept at each end; `false` shows the full value. Default [10, 8]. */
  truncate?: [number, number] | false;
  copy?: boolean;
  labels?: Partial<Pick<CommonWidgetLabels, 'copy' | 'copied' | 'explorer'>>;
  onCopy?: (value: string) => void;
};

/** Shortened address or hash with copy and explorer link. */
export function AddressDisplay({
  value,
  href,
  truncate = [10, 8],
  copy = true,
  labels,
  onCopy,
}: AddressDisplayProps) {
  const l = useWidgetLabels(COMMON_LABELS, labels);
  const shown = truncate ? truncateAddress(value, truncate[0], truncate[1]) : value;
  return (
    <span className="sqc-address">
      <span className="sqc-address__value" title={value}>
        {shown}
      </span>
      {copy ? <CopyButton text={value} labels={l} onCopied={onCopy} /> : null}
      {href ? (
        <a href={href} target="_blank" rel="noreferrer" className="sqc-account-link">
          {l.explorer}
        </a>
      ) : null}
    </span>
  );
}
