// Stand-ins for the GitHub REST API and the npm registry, so the admin release flow runs
// end to end without credentials. The API under test is pointed here with GITHUB_API_URL
// and NPM_REGISTRY_URL. GET /__dispatches lists the workflow_dispatch calls it received.
import { createServer } from 'node:http';

const port = Number(process.argv[2] ?? 8798);
const dispatches = [];
const versions = {
  'sdk/sqc-sdk/package.json': '3.0.0',
  'sdk/connect-react/package.json': '3.1.0',
  'packages/dapp-spec/package.json': '0.1.0',
  'packages/create-splendor-dapp/package.json': '0.1.0',
};
const changeset = '---\n"@splendor/connect-react": minor\n---\n\nAdd the widget manifest subpath.\n';
const distTags = {
  '@splendor/sqc-sdk': { latest: '3.0.0' },
  '@splendor/connect-react': { latest: '3.0.0', next: '3.1.0-next-20261001000000' },
};

const send = (res, status, body) => {
  res.writeHead(status, { 'content-type': 'application/json' });
  res.end(body === undefined ? '' : JSON.stringify(body));
};
const file = (text) => ({ content: Buffer.from(text).toString('base64'), encoding: 'base64' });

// "Push to GitHub": the OAuth web flow and the Git Data API for one user. GET /__pushes lists
// what was committed (repo, files, binary files).
const USER_TOKEN = 'gho_e2e_user';
const userRepos = new Map();
const pushes = [];
let sha = 0;
const readBody = (req) =>
  new Promise((resolve) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => resolve(raw ? JSON.parse(raw) : {}));
  });

async function githubUser(req, res, path, url) {
  if (path === '/github-web/login/oauth/authorize') {
    const back = new URL(url.searchParams.get('redirect_uri'));
    back.searchParams.set('code', 'e2e-code');
    back.searchParams.set('state', url.searchParams.get('state') ?? '');
    res.writeHead(302, { location: back.toString() });
    return res.end();
  }
  if (path === '/github-web/login/oauth/access_token') {
    const body = await readBody(req);
    return send(res, 200, body.code === 'e2e-code' ? { access_token: USER_TOKEN, scope: 'public_repo', token_type: 'bearer' } : { error: 'bad_verification_code' });
  }
  if (/^\/github\/applications\/[^/]+\/grant$/.test(path)) return send(res, 204);
  if (req.headers.authorization !== `Bearer ${USER_TOKEN}`) return send(res, 401, { message: 'Bad credentials' });
  if (path === '/github/user') return send(res, 200, { login: 'e2e-user' });
  const repoJson = (name) => ({
    name,
    full_name: `e2e-user/${name}`,
    html_url: `https://github.com/e2e-user/${name}`,
    default_branch: 'main',
    owner: { login: 'e2e-user' },
    permissions: { push: true },
  });
  if (path === '/github/user/repos' && req.method === 'POST') {
    const body = await readBody(req);
    userRepos.set(body.name, { head: `init-${++sha}`, blobs: new Map() });
    return send(res, 201, repoJson(body.name));
  }
  const m = /^\/github\/repos\/e2e-user\/([^/]+)(\/.*)?$/.exec(path);
  const repo = m && userRepos.get(m[1]);
  if (!repo) return send(res, 404, { message: 'Not Found' });
  const rest = m[2] ?? '';
  if (!rest) return send(res, 200, repoJson(m[1]));
  if (rest === '/git/ref/heads/main') return send(res, 200, { object: { sha: repo.head } });
  const body = req.method === 'GET' ? {} : await readBody(req);
  if (rest === '/git/blobs') {
    const id = `blob-${++sha}`;
    repo.blobs.set(id, body.content.length);
    return send(res, 201, { sha: id });
  }
  if (rest === '/git/trees') {
    repo.tree = body.tree;
    return send(res, 201, { sha: `tree-${++sha}` });
  }
  if (rest === '/git/commits') return send(res, 201, { sha: `commit-${++sha}` });
  if (rest === '/git/refs/heads/main' && req.method === 'PATCH') {
    repo.head = body.sha;
    pushes.push({
      repo: m[1],
      files: repo.tree.map((e) => e.path).sort(),
      binary: repo.tree.filter((e) => e.sha).map((e) => e.path),
    });
    return send(res, 200, { object: { sha: body.sha } });
  }
  return send(res, 404, { message: 'Not Found' });
}

createServer((req, res) => {
  const url = new URL(req.url ?? '/', `http://127.0.0.1:${port}`);
  const path = decodeURIComponent(url.pathname);
  if (path === '/__health') return send(res, 200, { ok: true });
  if (path === '/__dispatches') return send(res, 200, dispatches);
  if (path === '/__pushes') return send(res, 200, pushes);
  if (path.startsWith('/github-web/') || path === '/github/user' || path.startsWith('/github/user/') || path.startsWith('/github/applications/') || path.startsWith('/github/repos/e2e-user/')) {
    void githubUser(req, res, path, url);
    return;
  }

  const npm = /^\/npm\/-\/package\/(.+)\/dist-tags$/.exec(path);
  if (npm) return distTags[npm[1]] ? send(res, 200, distTags[npm[1]]) : send(res, 404, { error: 'not found' });

  const gh = /^\/github\/repos\/[^/]+\/[^/]+(\/.*)$/.exec(path);
  if (!gh) return send(res, 404, { message: 'Not Found' });
  if (req.headers.authorization !== 'Bearer e2e-token') return send(res, 401, { message: 'Bad credentials' });
  const route = gh[1];

  if (route === '/contents/.changeset') {
    return send(res, 200, [
      { name: 'README.md', path: '.changeset/README.md', type: 'file' },
      { name: 'widget-manifest.md', path: '.changeset/widget-manifest.md', type: 'file' },
    ]);
  }
  if (route === '/contents/.changeset/widget-manifest.md') return send(res, 200, file(changeset));
  const content = /^\/contents\/(.+)$/.exec(route);
  if (content && versions[content[1]]) return send(res, 200, file(JSON.stringify({ version: versions[content[1]] })));

  const wf = /^\/actions\/workflows\/([^/]+)\/(runs|dispatches)$/.exec(route);
  if (wf && wf[2] === 'runs') {
    const runs = dispatches.map((d, i) => ({
      id: 1000 + i,
      status: 'queued',
      conclusion: null,
      html_url: `https://github.com/example/actions/runs/${1000 + i}`,
      created_at: d.at,
      event: 'workflow_dispatch',
      display_title: `Release packages (${d.body.inputs.channel}${d.body.inputs.dry_run === 'true' ? ', dry run' : ''})`,
    }));
    return send(res, 200, { total_count: runs.length, workflow_runs: runs.reverse() });
  }
  if (wf && wf[2] === 'dispatches' && req.method === 'POST') {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      dispatches.push({ workflow: wf[1], body: JSON.parse(raw || '{}'), at: new Date().toISOString() });
      send(res, 204);
    });
    return;
  }
  send(res, 404, { message: 'Not Found' });
}).listen(port, '127.0.0.1', () => console.log(`fake GitHub/npm on :${port}`));
