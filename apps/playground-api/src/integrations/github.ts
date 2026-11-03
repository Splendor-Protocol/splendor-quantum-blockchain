import type { ApiConfig } from '../config.js';
import type { FetchLike } from '../releases.js';

const TIMEOUT_MS = 20_000;

/** A GitHub answer that was not 2xx. `status` 0: GitHub did not answer. */
export class GitHubError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = 'GitHubError';
  }
}

type Json = Record<string, unknown>;

async function call<T = Json>(fetchImpl: FetchLike, url: string, init: RequestInit, label: string): Promise<T> {
  let res: Response;
  try {
    res = await fetchImpl(url, { ...init, signal: AbortSignal.timeout(TIMEOUT_MS) });
  } catch (e) {
    const name = (e as { name?: string })?.name;
    throw new GitHubError(name === 'TimeoutError' || name === 'AbortError' ? `${label}: GitHub did not answer in time` : `${label}: GitHub is not reachable`, 0);
  }
  const text = await res.text();
  let body: Json | null = null;
  try {
    body = text ? (JSON.parse(text) as Json) : null;
  } catch {
    body = null;
  }
  if (!res.ok) {
    const message = typeof body?.message === 'string' ? body.message.slice(0, 200) : res.statusText;
    throw new GitHubError(`${label}: ${res.status} ${message}`, res.status);
  }
  return (body ?? {}) as T;
}

/** REST calls with a user's token. Paths are relative to the API base (`GITHUB_API_URL`). */
function userClient(fetchImpl: FetchLike, apiUrl: string, token: string) {
  return <T = Json>(method: string, path: string, body?: unknown) =>
    call<T>(
      fetchImpl,
      `${apiUrl}${path}`,
      {
        method,
        headers: {
          accept: 'application/vnd.github+json',
          'x-github-api-version': '2022-11-28',
          'user-agent': 'splendor-playground-api',
          authorization: `Bearer ${token}`,
          ...(body === undefined ? {} : { 'content-type': 'application/json' }),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      },
      `GitHub ${method} ${path.split('?')[0]}`,
    );
}

/** OAuth web flow, step 2: the code from the redirect for a token, then who it belongs to. */
export async function exchangeCode(
  fetchImpl: FetchLike,
  oauth: NonNullable<ApiConfig['githubOAuth']>,
  apiUrl: string,
  code: string,
): Promise<{ token: string; scope: string; login: string }> {
  const res = await call<{ access_token?: string; scope?: string; error?: string; error_description?: string }>(
    fetchImpl,
    `${oauth.webUrl}/login/oauth/access_token`,
    {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json', 'user-agent': 'splendor-playground-api' },
      body: JSON.stringify({
        client_id: oauth.clientId,
        client_secret: oauth.clientSecret,
        code,
        ...(oauth.callbackUrl ? { redirect_uri: oauth.callbackUrl } : {}),
      }),
    },
    'GitHub token exchange',
  );
  // GitHub answers 200 with an `error` field for a bad or used code.
  if (!res.access_token) throw new GitHubError(`GitHub token exchange: ${res.error ?? 'no token'}`, 400);
  const user = await userClient(fetchImpl, apiUrl, res.access_token)<{ login?: string }>('GET', '/user');
  if (typeof user.login !== 'string' || !/^[A-Za-z0-9-]{1,39}$/.test(user.login)) throw new GitHubError('GitHub /user: no login', 502);
  return { token: res.access_token, scope: res.scope ?? '', login: user.login };
}

/** Best effort: tell GitHub to forget the grant, so a disconnected token stops working everywhere. */
export async function revokeToken(fetchImpl: FetchLike, oauth: NonNullable<ApiConfig['githubOAuth']>, apiUrl: string, token: string): Promise<void> {
  const basic = Buffer.from(`${oauth.clientId}:${oauth.clientSecret}`).toString('base64');
  await call(
    fetchImpl,
    `${apiUrl}/applications/${encodeURIComponent(oauth.clientId)}/grant`,
    {
      method: 'DELETE',
      headers: {
        accept: 'application/vnd.github+json',
        'user-agent': 'splendor-playground-api',
        authorization: `Basic ${basic}`,
        'content-type': 'application/json',
      },
      body: JSON.stringify({ access_token: token }),
    },
    'GitHub revoke',
  );
}

export type PushFile = { path: string; content: string; encoding: 'utf-8' | 'base64' };

export type PushResult = { repoUrl: string; fullName: string; branch: string; commitSha: string; created: boolean };

type RepoJson = { name: string; full_name: string; html_url: string; default_branch?: string; owner: { login: string }; permissions?: { push?: boolean } };

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Creates `login/repo` (or reuses it when the user can push to it) and commits `files` as the whole
 * tree of the default branch, in one new commit on top of the current head: history is kept, and
 * files the export no longer has are removed. Binary files go up as base64 blobs, text inline.
 */
export async function pushToGitHub(
  fetchImpl: FetchLike,
  apiUrl: string,
  token: string,
  input: { login: string; repo: string; private: boolean; description: string; message: string; files: PushFile[] },
): Promise<PushResult> {
  const gh = userClient(fetchImpl, apiUrl, token);
  const enc = encodeURIComponent;
  let repo: RepoJson | null = null;
  let created = false;
  try {
    repo = await gh<RepoJson>('GET', `/repos/${enc(input.login)}/${enc(input.repo)}`);
  } catch (e) {
    if (!(e instanceof GitHubError && e.status === 404)) throw e;
  }
  if (repo && repo.permissions && repo.permissions.push === false) {
    throw new GitHubError(`You cannot push to ${repo.full_name}`, 403);
  }
  if (!repo) {
    // auto_init gives the repo a first commit: the Git Data API refuses an empty repository.
    repo = await gh<RepoJson>('POST', '/user/repos', {
      name: input.repo,
      private: input.private,
      description: input.description,
      auto_init: true,
      has_wiki: false,
    });
    created = true;
  }
  const base = `/repos/${enc(repo.owner.login)}/${enc(repo.name)}`;
  const branch = repo.default_branch || 'main';

  let head: string | null = null;
  for (let attempt = 0; attempt < 5 && !head; attempt++) {
    try {
      head = (await gh<{ object: { sha: string } }>('GET', `${base}/git/ref/heads/${enc(branch)}`)).object.sha;
    } catch (e) {
      if (!(e instanceof GitHubError && (e.status === 404 || e.status === 409))) throw e;
      // A new repository's first commit can take a moment to show up.
      if (!created) break;
      await sleep(300 * (attempt + 1));
    }
  }
  if (!head) throw new GitHubError(`${repo.full_name} has no commits yet: add a README on GitHub, or push to a new repository name`, 409);

  const tree: { path: string; mode: '100644'; type: 'blob'; sha?: string; content?: string }[] = [];
  const binary = input.files.filter((f) => f.encoding === 'base64');
  const shas = new Map<string, string>();
  for (let i = 0; i < binary.length; i += 4) {
    await Promise.all(
      binary.slice(i, i + 4).map(async (f) => {
        const blob = await gh<{ sha: string }>('POST', `${base}/git/blobs`, { content: f.content, encoding: 'base64' });
        shas.set(f.path, blob.sha);
      }),
    );
  }
  for (const f of input.files) {
    tree.push(f.encoding === 'base64' ? { path: f.path, mode: '100644', type: 'blob', sha: shas.get(f.path)! } : { path: f.path, mode: '100644', type: 'blob', content: f.content });
  }
  const newTree = await gh<{ sha: string }>('POST', `${base}/git/trees`, { tree });
  const commit = await gh<{ sha: string }>('POST', `${base}/git/commits`, { message: input.message, tree: newTree.sha, parents: [head] });
  await gh('PATCH', `${base}/git/refs/heads/${enc(branch)}`, { sha: commit.sha, force: false });
  const repoUrl = /^https:\/\/[^\s"'<>]+$/.test(repo.html_url) ? repo.html_url : `https://github.com/${repo.full_name}`;
  return { repoUrl, fullName: repo.full_name, branch, commitSha: commit.sha, created };
}
