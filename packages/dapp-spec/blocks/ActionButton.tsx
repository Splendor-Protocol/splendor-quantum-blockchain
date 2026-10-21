export type ActionButtonProps = {
  label?: string;
  /** Page path (e.g. /app) or https URL. */
  href?: string;
  variant?: string;
};

/** Button that navigates to a page hash or opens a URL. */
export function ActionButton({ label = 'Continue', href = '', variant = 'primary' }: ActionButtonProps) {
  const className = `sqc-btn${variant === 'ghost' ? ' sqc-btn--ghost' : ''}`;
  if (!href) {
    return (
      <button type="button" className={className} data-sqc-widget="action-button" disabled>
        {label}
      </button>
    );
  }
  if (href.startsWith('http')) {
    return (
      <a className={className} href={href} target="_blank" rel="noreferrer" data-sqc-widget="action-button">
        {label}
      </a>
    );
  }
  const hash = href.startsWith('#') ? href : `#${href.startsWith('/') ? href : `/${href}`}`;
  return (
    <a className={className} href={hash} data-sqc-widget="action-button">
      {label}
    </a>
  );
}
