import { useState } from 'react';
import {
  useAccount,
  useBridge,
  useContract,
  useDeployBytecode,
  useDeployCollection,
  useModuleCall,
  useModules,
  useQRC1155,
  useWallet,
} from '@splendor/connect-react';
import { Btn, Card, Field, Hint, Result, Row, toJson, useAction } from './ui';

function parseArgs(raw: string): unknown[] {
  const t = raw.trim();
  if (!t) return [];
  const v: unknown = JSON.parse(t);
  if (!Array.isArray(v)) throw new Error('Args must be a JSON array, e.g. ["SQ:...", "10"]');
  return v;
}

export function Qrc1155Card({ step }: { step: number }) {
  const { address: me } = useAccount();
  const deploy = useDeployCollection();
  const [name, setName] = useState('Lab Collection');
  const [uri, setUri] = useState('https://example.com/meta/');
  const [collection, setCollection] = useState('');
  const c = useQRC1155(collection.trim() || null);
  const read = useAction();
  const [id, setId] = useState('1');
  const [amount, setAmount] = useState('10');
  const [to, setTo] = useState('');

  const onDeploy = async () => {
    try {
      const out = await deploy.deploy({ name, uriPrefix: uri, waitForConfirmation: true });
      setCollection(out.address);
    } catch {
      /* shown via deploy.error */
    }
  };

  return (
    <Card
      id="qrc1155"
      step={step}
      title="QRC-1155 collection (useDeployCollection, useQRC1155)"
      tag="Wallet + SPLD"
      checks={[
        'Deploy collection: the result shows the new collection address and tx hash, and the address is copied into "Collection address".',
        'Collection info: returns the collection record with the name and URI prefix you deployed.',
        'Mint to me with id 1 and amount 10, then Balance of me: returns "10".',
        'Transfer 3 to another SQ: address, then Balance of me: returns "7".',
      ]}
    >
      <Row>
        <Field label="Name" value={name} onChange={setName} mono={false} />
        <Field label="URI prefix" value={uri} onChange={setUri} />
      </Row>
      <Btn onClick={() => void onDeploy()} disabled={deploy.status === 'pending'}>
        {deploy.status === 'pending' ? 'Deploying…' : 'Deploy collection'}
      </Btn>
      <Result
        status={deploy.status}
        data={{ address: deploy.address, hash: deploy.hash }}
        error={deploy.error?.message}
      />

      <Field label="Collection address" value={collection} onChange={setCollection} />
      <Row>
        <Field label="Token id" value={id} onChange={setId} />
        <Field label="Amount" value={amount} onChange={setAmount} />
      </Row>
      <Field label="Transfer to (SQ:…)" value={to} onChange={setTo} />
      <Row>
        <Btn kind="ghost" disabled={!collection} onClick={() => void read.run(() => c.collectionInfo())}>
          Collection info
        </Btn>
        <Btn
          kind="ghost"
          disabled={!collection || !me}
          onClick={() => void read.run(() => c.balanceOf(me!, id))}
        >
          Balance of me
        </Btn>
        <Btn
          disabled={!collection || !me}
          onClick={() => void c.mint(me!, id, amount).catch(() => undefined)}
        >
          Mint to me
        </Btn>
        <Btn
          disabled={!collection || !to.trim()}
          onClick={() => void c.transfer(to.trim(), id, amount).catch(() => undefined)}
        >
          Transfer
        </Btn>
      </Row>
      <Result {...read} />
      <Result
        status={c.status}
        data={{ hash: c.hash, explorer: c.explorerUrl }}
        error={c.error?.message}
      />
    </Card>
  );
}

export function ContractCard({ step }: { step: number }) {
  const deploy = useDeployBytecode();
  const [bytecode, setBytecode] = useState('');
  const [addr, setAddr] = useState('');
  const [sig, setSig] = useState('get()');
  const [args, setArgs] = useState('[]');
  const ct = useContract(addr.trim() || null);
  const read = useAction();

  const onDeploy = async () => {
    try {
      const out = await deploy.deploy(bytecode.trim(), { waitForConfirmation: true });
      setAddr(out.address);
    } catch {
      /* shown via deploy.error */
    }
  };

  return (
    <Card
      id="contract"
      step={step}
      title="SQC-VM contract (useDeployBytecode, useContract)"
      tag="Wallet + SPLD + bytecode"
      checks={[
        'Paste compiled bytecode (hex, e.g. from the QSol compiler in /compiler) and deploy: the contract address fills in below.',
        'read("get()"): returns return_data with reverted: false.',
        'readUint256("get()"): returns the value decoded as a bigint (shown with an "n" suffix).',
        'call("set(uint256)", ["42"]): returns a tx hash; readUint256("get()") then returns 42n (decimal, not 0x42 = 66).',
        'call("set(uint256)", ["0x2a"]): hex needs the 0x prefix; readUint256("get()") returns 42n again.',
        'Args ["ff"] or ["hello"]: fails before signing with "Unsupported ABI string arg" (bare hex is not guessed).',
        'Disconnect the wallet: read and readUint256 still work (reads use the network RPC); call asks you to connect.',
        'A function the contract does not have: fails with the revert reason from the node.',
      ]}
    >
      <label className="lab-field">
        <span>Bytecode (hex)</span>
        <textarea
          className="mono"
          rows={3}
          value={bytecode}
          onChange={(e) => setBytecode(e.target.value)}
          placeholder="0x6080…"
          spellCheck={false}
        />
      </label>
      <Btn onClick={() => void onDeploy()} disabled={!bytecode.trim() || deploy.status === 'pending'}>
        {deploy.status === 'pending' ? 'Deploying…' : 'Deploy bytecode'}
      </Btn>
      <Result
        status={deploy.status}
        data={{ address: deploy.address, hash: deploy.hash }}
        error={deploy.error?.message}
      />
      <Field label="Contract address" value={addr} onChange={setAddr} />
      <Row>
        <Field label="Function signature" value={sig} onChange={setSig} />
        <Field label="Args (JSON array)" value={args} onChange={setArgs} />
      </Row>
      <Row>
        <Btn kind="ghost" disabled={!addr} onClick={() => void read.run(() => ct.read(sig, parseArgs(args)))}>
          read
        </Btn>
        <Btn
          kind="ghost"
          disabled={!addr}
          onClick={() => void read.run(() => ct.readUint256(sig, parseArgs(args)))}
        >
          readUint256
        </Btn>
        <Btn
          disabled={!addr}
          onClick={() =>
            void read.run(() => ct.call(sig, parseArgs(args), { waitForConfirmation: true }))
          }
        >
          call (write)
        </Btn>
      </Row>
      <Result {...read} />
    </Card>
  );
}

export function BridgeCard({ step }: { step: number }) {
  const [addr, setAddr] = useState('');
  const [claimId, setClaimId] = useState('1');
  const bridge = useBridge(addr.trim() || null);
  const read = useAction();
  return (
    <Card
      id="bridge"
      step={step}
      title="Bridge reads (useBridge)"
      tag="Needs bridge address"
      checks={[
        'Paste the SQ: address of a deployed bridge (from the relayer or deploy logs).',
        'Bridge info: returns the bridge record, including the EVM chain id and the attestation threshold.',
        'Claim status: returns whether the EVM burn with that id has been claimed.',
        'An empty or wrong address: fails with an error instead of returning data.',
      ]}
    >
      <Hint>
        A claim needs signed attestations from the bridge relayer, so this card only covers reads.
        In an app, pass the relayer output to <code>useBridge(address).claim(…)</code>.
      </Hint>
      <Field label="Bridge address" value={addr} onChange={setAddr} />
      <Row>
        <Field label="Claim id" value={claimId} onChange={setClaimId} />
      </Row>
      <Row>
        <Btn kind="ghost" disabled={!addr} onClick={() => void read.run(() => bridge.info())}>
          Bridge info
        </Btn>
        <Btn
          kind="ghost"
          disabled={!addr}
          onClick={() => void read.run(() => bridge.claimed(Number(claimId)))}
        >
          Claim status
        </Btn>
      </Row>
      <Result {...read} />
    </Card>
  );
}

/** Routed chain modules and the fork that switches each one on (vm.KnownModuleActions). */
const CHAIN_MODULES: Array<{ id: string; fork: string | null; client: string }> = [
  { id: 'qrc20', fork: null, client: 'useQRC20 / useModules().qrc20' },
  { id: 'qrc1155', fork: 'qrc1155_v1', client: 'useQRC1155 / useModules().qrc1155' },
  { id: 'bridge', fork: 'bridge_v1', client: 'useBridge / useModules().bridge' },
  { id: 'validators', fork: 'validators_v1', client: 'useModuleCall' },
  { id: 'verify', fork: 'verify_v1', client: 'useModuleCall' },
  { id: 'qrc721', fork: 'qrc721_v1', client: 'useModuleCall' },
];

function moduleAvailability(status: Record<string, unknown> | null) {
  if (!status) return null;
  const forks = (status.forks ?? {}) as Record<string, number>;
  const height = Number(status.height ?? 0);
  return CHAIN_MODULES.map((m) => {
    if (!m.fork) return { module: m.id, live: true, client: m.client };
    const at = forks[m.fork];
    const live = typeof at === 'number' && height >= at;
    return {
      module: m.id,
      live,
      ...(typeof at === 'number' ? { activatesAt: at } : { note: `${m.fork} not scheduled on this network` }),
      client: m.client,
    };
  });
}

const MODULE_PRESETS: Array<{ label: string; module: string; action: string; params: Record<string, string> }> = [
  { label: 'qrc20 transfer', module: 'qrc20', action: 'transfer', params: { token: '', to: '', amount: '1' } },
  { label: 'qrc20 approve', module: 'qrc20', action: 'approve', params: { token: '', spender: '', amount: '1' } },
  { label: 'qrc1155 mint', module: 'qrc1155', action: 'mint', params: { collection: '', to: '', id: '1', amount: '1' } },
];

const ADDRESS_PARAMS = ['token', 'collection', 'to', 'from', 'spender', 'operator', 'bridge'];

function paramsProblem(p: unknown): string | null {
  if (!p || typeof p !== 'object' || Array.isArray(p)) return 'must be a JSON object';
  for (const key of ADDRESS_PARAMS) {
    const v = (p as Record<string, unknown>)[key];
    if (typeof v === 'string' && !v.trim()) return `"${key}" is empty; paste an SQ: address`;
  }
  return null;
}

export function ModulesCard({ step }: { step: number }) {
  const modules = useModules();
  const { provider } = useWallet();
  const status = useAction<Record<string, unknown>>();
  const { call, status: callStatus, hash, error } = useModuleCall();
  const [mod, setMod] = useState(MODULE_PRESETS[0].module);
  const [action, setAction] = useState(MODULE_PRESETS[0].action);
  const [params, setParams] = useState(JSON.stringify(MODULE_PRESETS[0].params, null, 2));
  const [parseErr, setParseErr] = useState<string | null>(null);

  const applyPreset = (p: (typeof MODULE_PRESETS)[number]) => {
    setMod(p.module);
    setAction(p.action);
    setParams(JSON.stringify(p.params, null, 2));
    setParseErr(null);
  };

  const onCall = () => {
    setParseErr(null);
    let p: unknown;
    try {
      p = JSON.parse(params);
    } catch (e) {
      setParseErr(e instanceof Error ? e.message : String(e));
      return;
    }
    const problem = paramsProblem(p);
    if (problem) {
      setParseErr(problem);
      return;
    }
    void call({
      module: mod.trim(),
      action: action.trim(),
      params: p as Record<string, unknown>,
      waitForConfirmation: true,
    }).catch(() => undefined);
  };

  const callError = error
    ? ((error as Error & { userHint?: string }).userHint ?? error.message)
    : undefined;

  const availability = moduleAvailability(status.data);

  return (
    <Card
      id="modules"
      step={step}
      title="Chain modules (useModules, useModuleCall)"
      tag="Wallet + SPLD"
      checks={[
        'After connecting, "typed" lists qrc20, qrc1155, bridge and contract, and "registered" lists the same ids from the SDK module registry.',
        'Check availability: each routed module shows live: true or false for this network, read from the node\'s fork schedule.',
        'Pick "qrc20 transfer", paste a real token and recipient, and send: returns a tx hash.',
        'Leave "token" or "to" empty, or break the JSON: shows a local error and nothing is sent.',
        'Clear Module or Action: the Send button is disabled.',
        'An unknown module or action, or a module that is not live: fails with the node\'s error.',
      ]}
    >
      <Hint>
        Native features such as tokens and the bridge run as modules inside the node, not as
        deployed bytecode. <code>useModules()</code> gives typed clients for the qrc20, qrc1155 and
        bridge modules, plus <code>contract</code> for SQC-VM bytecode contracts. The node also routes <code>validators</code>, <code>verify</code> and{' '}
        <code>qrc721</code>, which have no typed client; send those with{' '}
        <code>useModuleCall({'{ module, action, params }'})</code>. Some modules are switched on by
        a fork, so they only work on networks where that fork is active.
      </Hint>
      <pre className="lab-out">
        {modules
          ? toJson({
              typed: Object.keys(modules).filter((k) => k !== 'registered' && k !== 'list'),
              registered: modules.registered,
            })
          : 'Connect a wallet to bind the module clients.'}
      </pre>
      <Row>
        <Btn
          kind="ghost"
          disabled={!provider}
          onClick={() => void status.run(() => provider!.getStatus())}
        >
          Check availability
        </Btn>
      </Row>
      {availability ? <pre className="lab-out">{toJson(availability)}</pre> : null}
      {status.status === 'error' ? <Result status="error" error={status.error} /> : null}

      <Row>
        {MODULE_PRESETS.map((p) => (
          <Btn key={p.label} kind="ghost" onClick={() => applyPreset(p)}>
            {p.label}
          </Btn>
        ))}
      </Row>
      <Row>
        <Field label="Module" value={mod} onChange={setMod} />
        <Field label="Action" value={action} onChange={setAction} />
      </Row>
      <label className="lab-field">
        <span>Params (JSON)</span>
        <textarea
          className="mono"
          rows={6}
          value={params}
          onChange={(e) => setParams(e.target.value)}
          spellCheck={false}
        />
      </label>
      <Hint>
        Fill the empty addresses with real <code>SQ:</code> addresses and keep values to plain
        ASCII; the wallet cannot sign other characters. qrc20 amounts are in the token&apos;s
        smallest unit, so with 18 decimals <code>"1000000000000000000"</code> is one whole token.
      </Hint>
      <Btn
        onClick={onCall}
        disabled={!modules || !mod.trim() || !action.trim() || callStatus === 'pending'}
      >
        {callStatus === 'pending' ? 'Sending…' : 'Send module call'}
      </Btn>
      {parseErr ? <Result status="error" error={`Params: ${parseErr}`} /> : null}
      <Result status={callStatus} data={{ hash }} error={callError} />
    </Card>
  );
}
