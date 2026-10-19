import type { ApiConfig } from './config.js';

export type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

/** Packages released by `.github/workflows/release-packages.yml`. */
export const RELEASED_PACKAGES = [
  { name: '@splendor/sqc-sdk', path: 'sdk/sqc-sdk/package.json' },
  { name: '@splendor/connect-react', path: 'sdk/connect-react/package.json' },
  { name: '@splendor/dapp-spec', path: 'packages/dapp-spec/package.json' },
  { name: 'create-splendor-dapp', path: 'packages/create-splendor-dapp/package.json' },
] as const;

export type ReleaseStatus = {
  configured: boolean;
  repo: string;
  packages: { name: string; repoVersion: string | null; distTags: Record<string, string> }[];
  changesets: { file: string; summary: string }[];
  runs: { id: number; status: string; conclusion: string | null; url: string; createdAt: string; event: string; title: string }[];
  errors: string[];
};

const CACHE_MS = 30_000;
const UPSTREAM_TIMEOUT_MS = 10_000;

/** fetch with a deadline, so a stalled GitHub or npm never holds an admin request open. */
async function timedFetch(fetchImpl: FetchLike, label: string, url: string, init: RequestInit = {}, timeoutMs = UPSTREAM_TIMEOUT_MS): Promise<Response> {
  try {
    return await fetchImpl(url, { ...init, signal: AbortSignal.timeout(timeoutMs) });
  } catch (e) {
    const name = (e as { name?: string })?.name;
    if (name === 'TimeoutError' || name === 'AbortError') throw new Error(`${label}: timed out after ${Math.round(timeoutMs / 1000)}s`);
    throw new Error(`${label}: ${(e as Error)?.message ?? String(e)}`);
  }
}

/**
 * Read-only release status plus `workflow_dispatch`. The GitHub token needs only
 * `actions:write` + `contents:read` on this repo; npm credentials live in GitHub secrets.
 */
export function createReleaseService(config: ApiConfig['github'], fetchImpl: FetchLike = fetch, opts: { timeoutMs?: number } = {}) {
  let cache: { at: number; value: ReleaseStatus } | null = null;
  const timeoutMs = opts.timeoutMs ?? UPSTREAM_TIMEOUT_MS;
  const gh = async (path: string, init: RequestInit = {}) => {
    const res = await timedFetch(fetchImpl, `GitHub ${path.split('?')[0]}`, `${config.apiUrl}/repos/${config.repo}${path}`, {
      ...init,
      headers: {
        accept: 'application/vnd.github+json',
        'x-github-api-version': '2022-11-28',
        'user-agent': 'splendor-playground-api',
        ...(config.token ? { authorization: `Bearer ${config.token}` } : {}),
        ...init.headers,
      },
    }, timeoutMs);
    if (!res.ok) throw new Error(`GitHub ${path.split('?')[0]}: ${res.status} ${res.statusText}`);
    return res;
  };
  const ghJson = async <T>(path: string) => (await (await gh(path)).json()) as T;
  const fileText = async (path: string) => {
    const r = await ghJson<{ content: string; encoding: string }>(`/contents/${path}?ref=${encodeURIComponent(config.ref)}`);
    return Buffer.from(r.content, r.encoding === 'base64' ? 'base64' : 'utf8').toString('utf8');
  };

  async function distTags(name: string): Promise<Record<string, string>> {
    const res = await timedFetch(fetchImpl, `npm ${name}`, `${config.npmRegistry}/-/package/${name.replace('/', '%2F')}/dist-tags`, {
      headers: { accept: 'application/json' },
    }, timeoutMs);
    if (res.status === 404) return {};
    if (!res.ok) throw new Error(`npm ${name}: ${res.status}`);
    return (await res.json()) as Record<string, string>;
  }

  async function status(force = false): Promise<ReleaseStatus> {
    if (!force && cache && Date.now() - cache.at < CACHE_MS) return cache.value;
    const errors: string[] = [];
    const configured = !!config.token;
    const packages = await Promise.all(
      RELEASED_PACKAGES.map(async (p) => {
        let repoVersion: string | null = null;
        let tags: Record<string, string> = {};
        if (configured) {
          try {
            repoVersion = (JSON.parse(await fileText(p.path)) as { version?: string }).version ?? null;
          } catch (e) {
            errors.push(String((e as Error).message));
          }
        }
        try {
          tags = await distTags(p.name);
        } catch (e) {
          errors.push(String((e as Error).message));
        }
        return { name: p.name, repoVersion, distTags: tags };
      }),
    );
    let changesets: ReleaseStatus['changesets'] = [];
    let runs: ReleaseStatus['runs'] = [];
    if (configured) {
      try {
        const dir = await ghJson<{ name: string; path: string; type: string }[]>(`/contents/.changeset?ref=${encodeURIComponent(config.ref)}`);
        const files = dir.filter((f) => f.type === 'file' && f.name.endsWith('.md') && f.name !== 'README.md').slice(0, 30);
        changesets = await Promise.all(
          files.map(async (f) => {
            const text = await fileText(f.path).catch(() => '');
            const body = text.replace(/^---[\s\S]*?---\s*/, '').trim();
            return { file: f.name, summary: body.split(/\r?\n/)[0]?.slice(0, 200) ?? '' };
          }),
        );
      } catch (e) {
        errors.push(String((e as Error).message));
      }
      try {
        const r = await ghJson<{ workflow_runs: { id: number; status: string; conclusion: string | null; html_url: string; created_at: string; event: string; display_title?: string }[] }>(
          `/actions/workflows/${encodeURIComponent(config.workflow)}/runs?per_page=10`,
        );
        runs = r.workflow_runs.map((w) => ({
          id: w.id,
          status: w.status,
          conclusion: w.conclusion,
          url: w.html_url,
          createdAt: w.created_at,
          event: w.event,
          title: w.display_title ?? '',
        }));
      } catch (e) {
        errors.push(String((e as Error).message));
      }
    }
    const value = { configured, repo: config.repo, packages, changesets, runs, errors };
    cache = { at: Date.now(), value };
    return value;
  }

  async function dispatch(input: { channel: 'latest' | 'next'; dryRun: boolean }): Promise<void> {
    if (!config.token) throw new Error('GITHUB_TOKEN is not configured');
    await gh(`/actions/workflows/${encodeURIComponent(config.workflow)}/dispatches`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ ref: config.ref, inputs: { channel: input.channel, dry_run: input.dryRun ? 'true' : 'false' } }),
    });
    cache = null;
  }

  return { status, dispatch };
}

export type ReleaseService = ReturnType<typeof createReleaseService>;
