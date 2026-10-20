import { useMemo, useState } from 'react';
import { generateProject, type CatalogEntry, type DappSpec, type Framework } from '@splendor/dapp-spec';
import { CodeBlock } from '../components/CodeBlock';
import { PACKAGE_VERSIONS } from '../config';
import { deployLinks, downloadZip, openInStackBlitz } from './export';

export function ExportPanel({ spec, catalog, valid }: { spec: DappSpec; catalog: readonly CatalogEntry[]; valid: boolean }) {
  const [framework, setFramework] = useState<Framework>('vite');
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [repoUrl, setRepoUrl] = useState('');
  const [peek, setPeek] = useState<string | null>(null);

  const project = useMemo(() => {
    if (!valid) return null;
    try {
      return generateProject(spec, { framework, catalog, versions: PACKAGE_VERSIONS });
    } catch (e) {
      return e instanceof Error ? e : new Error(String(e));
    }
  }, [spec, framework, catalog, valid]);

  const run = async (label: string, fn: () => Promise<void>) => {
    setBusy(label);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const links = deployLinks(repoUrl);
  const ready = project && !(project instanceof Error) ? project : null;

  return (
    <div className="pg-stack" data-testid="export-panel">
      <div className="pg-segmented" role="group" aria-label="Framework">
        {(['vite', 'next'] as const).map((f) => (
          <button key={f} type="button" aria-pressed={framework === f} onClick={() => setFramework(f)}>
            {f === 'vite' ? 'Vite + React' : 'Next.js'}
          </button>
        ))}
      </div>
      {!valid ? <p className="pg-error">Fix the issues listed under the preview before exporting.</p> : null}
      {project instanceof Error ? <p className="pg-error">{project.message}</p> : null}
      <p className="pg-muted" style={{ margin: 0 }}>
        Pins <code>@splendor/connect-react@^{PACKAGE_VERSIONS.connectReact}</code> and <code>@splendor/sqc-sdk@^{PACKAGE_VERSIONS.sqcSdk}</code> from npm.
      </p>
      <button type="button" className="pg-btn" disabled={!ready || !!busy} data-testid="export-zip" onClick={() => ready && void run('zip', () => downloadZip(ready))}>
        {busy === 'zip' ? 'Preparing…' : 'Download .zip'}
      </button>
      <button
        type="button"
        className="pg-btn pg-btn-ghost"
        disabled={!ready || !!busy}
        onClick={() => ready && void run('stackblitz', () => openInStackBlitz(ready, spec.name))}
      >
        Open in StackBlitz
      </button>
      {error ? <p className="pg-error">{error}</p> : null}

      <h3>Deploy</h3>
      <p className="pg-muted" style={{ margin: 0 }}>
        Unzip, then deploy from your machine:
      </p>
      <CodeBlock
        code={
          framework === 'vite'
            ? `npm install\nnpm run build\n# Vercel\nnpx vercel deploy --prod\n# or Netlify\nnpx netlify deploy --prod --dir=dist`
            : `npm install\n# Vercel (recommended for Next.js)\nnpx vercel deploy --prod\n# or Netlify\nnpx netlify deploy --prod --build`
        }
      />
      <div className="pg-field">
        <label htmlFor="repo-url">Or push it to a Git repo and deploy with one click</label>
        <input
          id="repo-url"
          type="url"
          inputMode="url"
          autoComplete="off"
          placeholder="https://github.com/you/my-dapp"
          value={repoUrl}
          aria-invalid={!!repoUrl.trim() && !links}
          aria-describedby="repo-url-hint"
          onChange={(e) => setRepoUrl(e.target.value)}
        />
        <small id="repo-url-hint" className={repoUrl.trim() && !links ? 'pg-error' : 'pg-muted'}>
          {repoUrl.trim() && !links
            ? 'Use a repository URL on github.com, gitlab.com or bitbucket.org, like https://github.com/you/my-dapp.'
            : 'The deploy buttons turn on once the URL is a repository.'}
        </small>
      </div>
      <div className="pg-row">
        <a className={`pg-btn pg-btn-ghost${links ? '' : ' is-disabled'}`} aria-disabled={!links} href={links?.vercel} target="_blank" rel="noreferrer noopener">
          Deploy to Vercel
        </a>
        <a className={`pg-btn pg-btn-ghost${links ? '' : ' is-disabled'}`} aria-disabled={!links} href={links?.netlify} target="_blank" rel="noreferrer noopener">
          Deploy to Netlify
        </a>
      </div>

      {ready ? (
        <details>
          <summary>Files ({Object.keys(ready.files).length})</summary>
          <ul className="pg-list">
            {Object.keys(ready.files).map((f) => (
              <li key={f}>
                <button type="button" className={peek === f ? 'is-active' : ''} onClick={() => setPeek(peek === f ? null : f)}>
                  <code>{f}</code>
                </button>
              </li>
            ))}
          </ul>
          {peek && ready.files[peek] != null ? <CodeBlock code={ready.files[peek]!} /> : null}
        </details>
      ) : null}
    </div>
  );
}
