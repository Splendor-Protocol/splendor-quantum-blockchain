import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { migrateSpec, validateSpec, type DappSpec } from '@splendor/dapp-spec';
import { api, ApiError } from '../api/client';
import { PreviewFrame } from '../preview/PreviewFrame';
import { useRegistry } from '../registry';
import { saveDraft, stashDraft } from './storage';

/** Read-only view of a shared project, with "Remix" to copy it into the builder. */
export function SharedPage() {
  const { shareId = '' } = useParams();
  const { all } = useRegistry();
  const navigate = useNavigate();
  const [spec, setSpec] = useState<DappSpec | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pagePath, setPagePath] = useState('/');

  useEffect(() => {
    let live = true;
    setError(null);
    setSpec(null);
    api
      .getShared(shareId)
      .then((r) => {
        const v = validateSpec(migrateSpec(r.spec), { catalog: all });
        if (!v.ok) throw new Error('This shared project cannot be opened here: it uses widgets or settings this playground does not support.');
        if (live) setSpec(v.spec);
      })
      .catch((e) => {
        if (!live) return;
        if (e instanceof ApiError && e.code === 'EXPIRED') setError('This share link has expired. Ask its owner for a new one.');
        else if (e instanceof ApiError && e.status === 404) setError('This share link was removed by its owner, or it never existed.');
        else setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      live = false;
    };
  }, [shareId, all]);

  if (error) {
    return (
      <div className="pg-page">
        <h1>Shared project unavailable</h1>
        <p className="pg-error" role="alert">
          {error}
        </p>
        <div className="pg-row">
          <Link className="pg-btn" to="/build">
            Start your own dApp
          </Link>
          <Link className="pg-btn pg-btn-ghost" to="/">
            Home
          </Link>
        </div>
      </div>
    );
  }
  if (!spec) return <p className="pg-pad pg-muted" role="status">Loading shared project…</p>;
  return (
    <div className="pg-page">
      <div className="pg-row">
        <h1 style={{ margin: 0 }}>{spec.name}</h1>
        <span className="pg-spacer" />
        <button
          type="button"
          className="pg-btn"
          onClick={() => {
            stashDraft();
            saveDraft({ spec, projectId: null });
            navigate('/build');
          }}
        >
          Remix in the builder
        </button>
      </div>
      {spec.description ? <p className="pg-muted">{spec.description}</p> : null}
      <p className="pg-issues" role="note" data-testid="shared-caution">
        Shared project: anyone can publish one. Check every address in it (recipients, tokens, collections) before you use it
        {spec.network === 'mainnet' ? ' on mainnet with real SPLD' : ' with real funds'}. The preview below uses a mock wallet.
      </p>
      <PreviewFrame spec={spec} pagePath={pagePath} network={spec.network} walletState="connected" onNavigate={setPagePath} height="70vh" />
    </div>
  );
}
