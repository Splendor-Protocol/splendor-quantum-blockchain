import { Fragment } from 'react';
import { Link } from 'react-router-dom';
import { kitTemplates, type DappTemplate } from '@splendor/dapp-spec';
import '../studio/preview-app.css';

/** Outline chips: "Landing (public) → Dashboard (wallet)". */
function Outline({ outline }: { outline: readonly string[] }) {
  return (
    <ul className="pg-kit-outline" aria-label="Pages">
      {outline.map((page, i) => (
        <Fragment key={`${page}-${i}`}>
          {i > 0 ? (
            <li className="pg-kit-arrow" aria-hidden="true">
              {'\u2192'}
            </li>
          ) : null}
          <li className="pg-kit-chip">{page}</li>
        </Fragment>
      ))}
    </ul>
  );
}

export type KitCardsProps = {
  /** The kits to feature. Defaults to `kitTemplates()`. */
  kits?: readonly DappTemplate[];
  /** Called with the kit (or template, or `blank`) id that was chosen. */
  onPick: (id: string) => void;
  /** Highlights the chosen card (`aria-pressed`). */
  selected?: string | null;
  /** Test ids are `${testIdPrefix}-${id}`. */
  testIdPrefix?: string;
  /** "Skip setup" link per kit, straight to the studio with the demo values. */
  demoHref?: (id: string) => string;
  /** Templates listed under the "Start from scratch / other templates" expander. Omit for no expander. */
  others?: readonly DappTemplate[];
  /** Adds a "Blank page" entry (id `blank`) to the expander. */
  includeBlank?: boolean;
  /** Whether the expander starts open. */
  othersOpen?: boolean;
};

/** Big cards for the ready-made app kits, and an expander with everything else. */
export function KitCards({
  kits = kitTemplates(),
  onPick,
  selected = null,
  testIdPrefix = 'kit',
  demoHref,
  others,
  includeBlank = false,
  othersOpen = false,
}: KitCardsProps) {
  const showOthers = (others && others.length > 0) || includeBlank;
  return (
    <div className="pg-kits" data-testid="kit-cards">
      <div className="pg-kit-grid">
        {kits.map((kit) => (
          <div key={kit.id} className={`pg-kit-card${selected === kit.id ? ' is-selected' : ''}`}>
            <button type="button" className="pg-kit-main" aria-pressed={selected === kit.id} data-testid={`${testIdPrefix}-${kit.id}`} onClick={() => onPick(kit.id)}>
              <span className="pg-kit-badge">Ready-made app</span>
              <h3>{kit.title}</h3>
              <p>{kit.description}</p>
              {kit.outline?.length ? <Outline outline={kit.outline} /> : null}
            </button>
            {demoHref ? (
              <Link className="pg-link-btn pg-kit-demo" to={demoHref(kit.id)} data-testid={`${testIdPrefix}-demo-${kit.id}`}>
                Skip setup and open the demo
              </Link>
            ) : null}
          </div>
        ))}
      </div>
      {showOthers ? (
        <details className="pg-kit-others" open={othersOpen || undefined} data-testid="kit-others">
          <summary>Start from scratch / other templates</summary>
          <div className="pg-cards" data-testid="kit-others-list">
            {(others ?? []).map((t) => (
              <button
                key={t.id}
                type="button"
                className={`pg-card pg-card-btn${selected === t.id ? ' is-selected' : ''}`}
                aria-pressed={selected === t.id}
                data-testid={`${testIdPrefix}-${t.id}`}
                onClick={() => onPick(t.id)}
              >
                <h3>{t.title}</h3>
                <p>{t.description}</p>
              </button>
            ))}
            {includeBlank ? (
              <button
                type="button"
                className={`pg-card pg-card-btn${selected === 'blank' ? ' is-selected' : ''}`}
                aria-pressed={selected === 'blank'}
                data-testid={`${testIdPrefix}-blank`}
                onClick={() => onPick('blank')}
              >
                <h3>Blank page</h3>
                <p>A heading, then an empty page.</p>
              </button>
            ) : null}
          </div>
        </details>
      ) : null}
    </div>
  );
}
