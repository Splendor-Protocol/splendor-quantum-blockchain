import { useEffect, useMemo, useState } from 'react';
import { generateProject, type CatalogEntry, type DappSpec, type Framework, type GeneratedProject } from '@splendor/dapp-spec';
import { CodeBlock } from '../components/CodeBlock';
import { PACKAGE_VERSIONS } from '../config';
import { deployLinks, downloadZip, openInStackBlitz } from './export';
import { loadVendorOptions, sdkSourceLabel } from './vendor';

const FILE_LIST_LIMIT = 80;

export function ExportPanel({
  spec,
  catalog,
  valid,
  setupLeft = [],
  onFixFirst,
}: {
  spec: DappSpec;
  catalog: readonly CatalogEntry[];
  valid: boolean;
  /** What "Finish setup" still lists, so the dialog can say it without sending the user behind itself. */
  setupLeft?: string[];
  /** Close the dialog and go to the first setup item. */
  onFixFirst?: () => void;
}) {
  const [framework, setFramework] = useState<Framework>('vite');
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [repoUrl, setRepoUrl] = useState('');
  const [peek, setPeek] = useState<string | null>(null);
  const [vendor, setVendor] = useState<Awaited<ReturnType<typeof loadVendorOptions>> | null>(null);

  useEffect(() => {
    void loadVendorOptions().then(setVendor);
  }, []);

  const project = useMemo(() => {
    if (!valid || !vendor) return null;
    try {
      return generateProject(spec, {
        framework,
        catalog,
        versions: PACKAGE_VERSIONS,
        ...vendor,
      });
    } catch (e) {
      return e instanceof Error ? e : new Error(String(e));
    }
  }, [spec, framework, catalog, valid, vendor]);

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
  const ready: GeneratedProject | null = project && !(project instanceof Error) ? project : null;
  const files = ready ? Object.keys(ready.files).filter((f) => !f.startsWith('vendor/') || f.endsWith('package.json')) : [];

  return (
    <div className="pg-stack" data-testid="export-panel">
      <div className="pg-segmented" role="group" aria-label="Framework">
        {(['vite', 'next'] as const).map((f) => (
          <button key={f} type="button" aria-pressed={framework === f} onClick={() => setFramework(f)}>
            {f === 'vite' ? 'Vite + React' : 'Next.js'}
          </button>
        ))}
      </div>
      {!valid ? (
        <div className="pg-error" role="alert" data-testid="export-setup-left">
          <p style={{ margin: 0 }}>Finish setup before exporting:</p>
          {setupLeft.length ? (
            <ul style={{ margin: '0.35rem 0' }}>
              {setupLeft.slice(0, 5).map((m) => (
                <li key={m}>{m}</li>
              ))}
              {setupLeft.length > 5 ? <li>…and {setupLeft.length - 5} more</li> : null}
            </ul>
          ) : null}
          {onFixFirst ? (
            <button type="button" className="pg-btn pg-btn-sm" data-testid="export-fix-first" onClick={onFixFirst}>
              Go to the first one
            </button>
          ) : null}
        </div>
      ) : null}
      {project instanceof Error ? <p className="pg-error">{project.message}</p> : null}
      <p className="pg-muted" style={{ margin: 0 }} role="status">
        {vendor ? (
          <>
            Uses the {sdkSourceLabel(vendor)}. Set <code>VITE_PLAYGROUND_SDK_SOURCE=npm</code> after publishing the packages.
          </>
        ) : (
          'Preparing the export…'
        )}
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
            {files.slice(0, FILE_LIST_LIMIT).map((f) => (
              <li key={f}>
                <button type="button" className={peek === f ? 'is-active' : ''} onClick={() => setPeek(peek === f ? null : f)}>
                  <code>{f}</code>
                </button>
              </li>
            ))}
          </ul>
          {files.length > FILE_LIST_LIMIT ? <p className="pg-muted">…and {files.length - FILE_LIST_LIMIT} more files in the zip.</p> : null}
          {peek && ready.files[peek] != null ? <CodeBlock code={ready.files[peek]!} /> : null}
        </details>
      ) : null}
    </div>
  );
}
