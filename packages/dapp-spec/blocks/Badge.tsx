export type BadgeProps = {
  text?: string;
  tone?: string;
};

const TONES: Record<string, { color: string; border: string }> = {
  neutral: { color: 'var(--sqc-text-muted, #8a93a6)', border: 'var(--sqc-border, #262b38)' },
  accent: { color: 'var(--sqc-accent, #7c5cff)', border: 'var(--sqc-accent, #7c5cff)' },
  success: { color: 'var(--sqc-success, #3ecf8e)', border: 'var(--sqc-success, #3ecf8e)' },
  danger: { color: 'var(--sqc-danger, #ff5d6c)', border: 'var(--sqc-danger, #ff5d6c)' },
};

/** Small pill label. */
export function Badge({ text = 'New', tone = 'accent' }: BadgeProps) {
  const t = TONES[tone] ?? TONES.neutral!;
  return (
    <span
      className="dapp-badge"
      data-sqc-widget="badge"
      style={{
        display: 'inline-block',
        padding: '0.15rem 0.7rem',
        borderRadius: 999,
        border: `1px solid ${t.border}`,
        color: t.color,
        fontSize: '0.8rem',
        fontWeight: 600,
      }}
    >
      {text}
    </span>
  );
}
