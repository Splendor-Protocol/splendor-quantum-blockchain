import { useCallback, useEffect, useState } from 'react';
import { slugify, type GeneratedProject } from '@splendor/dapp-spec';
import { api, ApiError, type GitHubPushResult, type GitHubStatus } from '../api/client';
import { useSession } from '../auth/session';
import { Notice } from '../components/Notice';
import { importLinks } from './export';

const CHANNEL = 'sqc-github-oauth';
const REPO_NAME_RE = /^[A-Za-z0-9._-]{1,100}$/;

/** Problem with a repository name, or null. Same rules as the API. */
export function repoNameError(name: string): string | null {
  const n = name.trim();
  if (!n) return 'Enter a repository name.';
  if (!REPO_NAME_RE.test(n)) return 'Use letters, digits, dots, dashes or underscores (up to 100).';
  if (n === '.' || n === '..' || /\.git$/i.test(n)) return 'Pick another name.';
  return null;
}

/**
 * Push the export to the user's own GitHub account (OAuth through the playground API), then offer
 * Vercel and Netlify imports of that repository. Hidden when the API has no GitHub app configured.
 */
export function GitHubPush({ project, appName, description }: { project: GeneratedProject | null; appName: string; description?: string }) {
  const { session, apiAvailable } = useSession();
  const [status, setStatus] = useState<GitHubStatus | null>(null);
  const [repo, setRepo] = useState(() => slugify(appName) || 'my-dapp');
  const [isPrivate, setIsPrivate] = useState(false);
  const [busy, setBusy] = useState<'connect' | 'push' | 'disconnect' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<GitHubPushResult | null>(null);

  const refresh = useCallback(async () => {
    if (!session) return setStatus(null);
    try {
      setStatus(await api.githubStatus());
    } catch {
      setStatus({ enabled: false, connected: false });
    }
  }, [session]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // The OAuth window reports back over a BroadcastChannel (and postMessage); focus is the fallback.
  useEffect(() => {
    const onResult = (data: unknown) => {
      const d = data as { type?: string; status?: string } | null;
      if (d?.type !== CHANNEL) return;
      setBusy(null);
      if (d.status === 'ok') setError(null);
      else setError(d.status === 'denied' ? 'GitHub access was not granted.' : 'Connecting GitHub failed. Try again.');
      void refresh();
    };
    const channel = typeof BroadcastChannel === 'function' ? new BroadcastChannel(CHANNEL) : null;
    if (channel) channel.onmessage = (e) => onResult(e.data);
    const onMessage = (e: MessageEvent) => {
      if (e.origin === window.location.origin) onResult(e.data);
    };
    const onFocus = () => void refresh();
    window.addEventListener('message', onMessage);
    window.addEventListener('focus', onFocus);
    return () => {
      channel?.close();
      window.removeEventListener('message', onMessage);
      window.removeEventListener('focus', onFocus);
    };
  }, [refresh]);

  if (!apiAvailable) return null;
  if (!session) {
    return (
      <p className="pg-muted" style={{ margin: 0 }} data-testid="github-signin-hint">
        Sign in (top right) to push this project to your GitHub account in one step.
      </p>
    );
  }
  if (!status?.enabled) return null;

  const fail = (e: unknown) => {
    if (e instanceof ApiError && e.code === 'GITHUB_RECONNECT') {
      setStatus((s) => (s ? { ...s, connected: false, account: null } : s));
    }
    setError(e instanceof Error ? e.message : String(e));
  };

  const connect = async () => {
    setBusy('connect');
    setError(null);
    try {
      const { url } = await api.githubStart();
      const win = window.open(url, 'sqc-github-oauth', 'popup,width=720,height=780');
      // Popup blocked: go in this tab. The draft is saved locally, and the page comes back to the studio.
      if (!win) window.location.assign(url);
    } catch (e) {
      setBusy(null);
      fail(e);
    }
  };

  const disconnect = async () => {
    setBusy('disconnect');
    setError(null);
    try {
      await api.githubDisconnect();
      setResult(null);
      await refresh();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(null);
    }
  };

  const nameError = repoNameError(repo);
  const push = async () => {
    if (!project || nameError) return;
    setBusy('push');
    setError(null);
    setResult(null);
    try {
      setResult(
        await api.githubPush({
          repo: repo.trim(),
          private: isPrivate,
          description: (description ?? `${appName} — a dApp on Splendor Quantum Chain`).slice(0, 350),
          message: 'Export from Splendor dApp Studio',
          files: project.files,
        }),
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy(null);
    }
  };

  const links = result ? importLinks(result.repoUrl) : null;
  // `public_repo` cannot create private repositories.
  const canPrivate = status.scope?.split(/[ ,]+/).includes('repo') ?? false;

  return (
    <section className="pg-github-push pg-stack" aria-labelledby="github-push-title" data-testid="github-push">
      <h4 id="github-push-title" style={{ margin: 0 }}>
        Push to GitHub
      </h4>
      {!status.connected ? (
        <>
          <p className="pg-muted" style={{ margin: 0 }}>
            Connect your GitHub account once; the studio creates a repository and commits the project for you.
          </p>
          <div className="pg-row">
            <button type="button" className="pg-btn" disabled={busy === 'connect'} data-testid="github-connect" onClick={() => void connect()}>
              {busy === 'connect' ? 'Waiting for GitHub…' : 'Connect GitHub'}
            </button>
            {busy === 'connect' ? (
              <button type="button" className="pg-btn pg-btn-ghost pg-btn-sm" onClick={() => void refresh().then(() => setBusy(null))}>
                I have connected
              </button>
            ) : null}
          </div>
        </>
      ) : (
        <>
          <p className="pg-muted" style={{ margin: 0 }} data-testid="github-account">
            Connected as <strong>{status.account}</strong>.{' '}
            <button type="button" className="pg-link-btn" disabled={busy === 'disconnect'} onClick={() => void disconnect()}>
              Disconnect
            </button>
          </p>
          <div className="pg-field">
            <label htmlFor="github-repo">Repository name</label>
            <input
              id="github-repo"
              value={repo}
              maxLength={100}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={!!nameError}
              aria-describedby="github-repo-hint"
              onChange={(e) => setRepo(e.target.value)}
            />
            <small id="github-repo-hint" className={nameError ? 'pg-error' : 'pg-muted'}>
              {nameError ?? `github.com/${status.account}/${repo.trim()}. An existing repository of yours gets a new commit with this export.`}
            </small>
          </div>
          {canPrivate ? (
            <label className="pg-row" style={{ gap: '0.4rem' }}>
              <input type="checkbox" checked={isPrivate} onChange={(e) => setIsPrivate(e.target.checked)} />
              Private repository
            </label>
          ) : null}
          <button type="button" className="pg-btn" disabled={!project || !!nameError || !!busy} data-testid="github-push-button" onClick={() => void push()}>
            {busy === 'push' ? 'Pushing…' : 'Push to GitHub'}
          </button>
        </>
      )}
      {error ? (
        <Notice tone="error" testId="github-error">
          {error}
        </Notice>
      ) : null}
      {result && links ? (
        <Notice tone="success" title={result.created ? 'Repository created' : 'Repository updated'} testId="github-result">
          <p style={{ margin: '0 0 0.5rem' }}>
            <a href={result.repoUrl} target="_blank" rel="noopener noreferrer" data-testid="github-repo-link">
              {result.fullName}
            </a>{' '}
            · branch <code>{result.branch}</code>
          </p>
          <div className="pg-row">
            <a className="pg-btn pg-btn-sm" href={links.vercel} target="_blank" rel="noopener noreferrer" data-testid="github-deploy-vercel">
              Deploy on Vercel
            </a>
            <a className="pg-btn pg-btn-ghost pg-btn-sm" href={links.netlify} target="_blank" rel="noopener noreferrer" data-testid="github-deploy-netlify">
              Deploy on Netlify
            </a>
          </div>
        </Notice>
      ) : null}
    </section>
  );
}
