import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { migrateSpec, validateSpec, type DappSpec } from '@splendor/dapp-spec';
import { api } from '../api/client';
import { PreviewFrame } from '../preview/PreviewFrame';
import { useRegistry } from '../registry';
import { saveDraft } from './storage';

/** Read-only view of a shared project, with "Remix" to copy it into the builder. */
export function SharedPage() {
  const { shareId = '' } = useParams();
  const { all } = useRegistry();
  const navigate = useNavigate();
  const [spec, setSpec] = useState<DappSpec | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pagePath, setPagePath] = useState('/');

  useEffect(() => {
    api
      .getShared(shareId)
      .then((r) => {
        const v = validateSpec(migrateSpec(r.spec), { catalog: all });
        if (!v.ok) throw new Error('This shared project uses widgets this playground does not have.');
        setSpec(v.spec);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, [shareId, all]);

  if (error) return <p className="pg-pad pg-error">{error}</p>;
  if (!spec) return <p className="pg-pad pg-muted">Loading shared project…</p>;
  return (
    <div className="pg-page">
      <div className="pg-row">
        <h1 style={{ margin: 0 }}>{spec.name}</h1>
        <span className="pg-spacer" />
        <button
          type="button"
          className="pg-btn"
          onClick={() => {
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
