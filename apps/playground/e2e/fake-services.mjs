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

createServer((req, res) => {
  const url = new URL(req.url ?? '/', `http://127.0.0.1:${port}`);
  const path = decodeURIComponent(url.pathname);
  if (path === '/__health') return send(res, 200, { ok: true });
  if (path === '/__dispatches') return send(res, 200, dispatches);

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
