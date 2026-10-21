export type FooterBarProps = { text?: string };

/** Simple page footer line. */
export function FooterBar({ text = 'Built with Splendor' }: FooterBarProps) {
  return (
    <footer className="sqc-widget dapp-footer-bar" data-sqc-widget="footer-bar" style={{ textAlign: 'center', padding: '1.5rem 0', color: 'var(--sqc-text-muted, #9aa4b2)' }}>
      <small>{text}</small>
    </footer>
  );
}
