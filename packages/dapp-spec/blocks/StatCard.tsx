export type StatCardProps = {
  label?: string;
  value?: string;
  hint?: string;
};

/** KPI / stat tile. */
export function StatCard({ label = 'Metric', value = '—', hint = '' }: StatCardProps) {
  return (
    <div className="sqc-widget dapp-stat-card" data-sqc-widget="stat-card" style={{ padding: '1rem' }}>
      <p className="sqc-widget__msg" style={{ margin: 0, fontSize: '0.8rem', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
        {label}
      </p>
      <p style={{ margin: '0.35rem 0 0', fontSize: '1.6rem', fontWeight: 700 }}>{value}</p>
      {hint ? <p className="sqc-widget__msg" style={{ margin: '0.25rem 0 0' }}>{hint}</p> : null}
    </div>
  );
}
