import type { ReactNode, SVGProps } from 'react';

type IconProps = SVGProps<SVGSVGElement> & { title?: string };

export function IconWallet(props: IconProps) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden {...props}>
      <path
        d="M3 8.5A2.5 2.5 0 0 1 5.5 6h13A2.5 2.5 0 0 1 21 8.5v7a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 15.5v-7Z"
        stroke="currentColor"
        strokeWidth="1.75"
      />
      <path d="M16 12h2" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" />
    </svg>
  );
}

export function IconMetaMask(props: IconProps) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden {...props}>
      <path
        d="M12 3 5 8l1.5 5L5 16l2 3 2.5-1.5L12 19l2.5-1.5L17 19l2-3-1.5-3L19 8l-7-5Z"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function IconKey(props: IconProps) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden {...props}>
      <circle cx="8" cy="14" r="3.5" stroke="currentColor" strokeWidth="1.75" />
      <path
        d="M11 12.5 18 5.5M15.5 8l2.5 2.5"
        stroke="currentColor"
        strokeWidth="1.75"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function IconClose(props: IconProps) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden {...props}>
      <path d="M6 6l12 12M18 6 6 18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  );
}

export function connectorIcon(id: string): ReactNode {
  if (id === 'metamask' || id === 'injected') return <IconMetaMask />;
  if (id === 'seed') return <IconKey />;
  return <IconWallet />;
}
