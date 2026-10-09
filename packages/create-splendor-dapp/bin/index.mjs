#!/usr/bin/env node
/**
 * npm create splendor-dapp@latest [name] -- [--template id] [--framework vite|next] [--network testnet]
 *                                           [--set block.prop=value] [--yes]
 */
import { createInterface } from 'node:readline/promises';
import { stdin, stdout } from 'node:process';
import { FRAMEWORKS, NETWORKS, TEMPLATES, buildProject, parseArgs, pinnedVersions, writeProject } from './lib.mjs';

function usage() {
  const { connectReact, sqcSdk } = pinnedVersions();
  console.log(`Create a Splendor Quantum Chain dApp.

Usage:
  npm create splendor-dapp@latest my-dapp -- --template tip-jar --framework vite

Options:
  -t, --template   ${TEMPLATES.map((t) => t.id).join(' | ')}
  -f, --framework  ${FRAMEWORKS.join(' | ')}            (default vite)
  -n, --network    ${NETWORKS.join(' | ')}  (default testnet)
      --set        block.prop=value, e.g. --set tips.recipient=SQ:…
  -y, --yes        accept defaults, no prompts

Pins @splendor/connect-react@^${connectReact} and @splendor/sqc-sdk@^${sqcSdk}.
Design visually instead: https://playground.splendor.org`);
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) return usage();
  const interactive = stdin.isTTY && !args.yes;
  const rl = interactive ? createInterface({ input: stdin, output: stdout }) : null;
  const ask = async (q, def) => (rl ? (await rl.question(`${q}${def ? ` (${def})` : ''}: `)).trim() || def : def);

  try {
    const name = args.name ?? (await ask('Project name', 'my-splendor-dapp'));
    if (!name || !/^[a-zA-Z0-9._-]+$/.test(name)) throw new Error('Project name: use letters, numbers, ., _ or -');

    let template = args.template;
    if (!template && rl) {
      TEMPLATES.forEach((t, i) => console.log(`  ${i + 1}. ${t.title} — ${t.description}`));
      const pick = await ask('Template number', '1');
      template = TEMPLATES[Number(pick) - 1]?.id ?? pick;
    }
    template ??= 'wallet-dashboard';
    const framework = args.framework ?? (await ask(`Framework (${FRAMEWORKS.join('/')})`, 'vite'));
    const network = args.network ?? (await ask(`Network (${NETWORKS.join('/')})`, 'testnet'));

    const set = [...args.set];
    const t = TEMPLATES.find((x) => x.id === template);
    for (const s of t?.setup ?? []) {
      if (set.some((x) => x.startsWith(`${s.blockId}.${s.prop}=`))) continue;
      const v = await ask(`${s.label} (SQ: address)`, '');
      if (v) set.push(`${s.blockId}.${s.prop}=${v}`);
    }

    const project = buildProject({ name, template, framework, network, set });
    const dir = writeProject(name, project.files);
    console.log(`
Created ${name}/ (${t?.title ?? template}, ${framework === 'vite' ? 'Vite' : 'Next.js'}, ${network})

  cd ${name}
  npm install
  npm run dev

Read ${name}/README.md for networks, the WASM signer and deployment.`);
    return dir;
  } finally {
    rl?.close();
  }
}

main().catch((e) => {
  console.error(`\n${e instanceof Error ? e.message : String(e)}`);
  process.exit(1);
});
