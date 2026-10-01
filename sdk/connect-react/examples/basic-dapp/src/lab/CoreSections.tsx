import { useState } from 'react';
import {
  AccountCard,
  AddressInput,
  AmountInput,
  ConnectButton,
  ReviewPanel,
  TxResult,
  WalletGate,
  connectErrorMessage,
  useSendTransfer,
  useTransferForm,
  DeployTokenWidget,
  NetworkBadge,
  SignInButton,
  TokenPanel,
  TransferWidget,
  WrongNetworkBanner,
  checkAuthMessage,
  compareDecimal,
  createAuthMessage,
  normalizeSqAddress,
  reviewTransfer,
  sanitizeIconUrl,
  useAccount,
  useAccountDto,
  useBalance,
  useConnect,
  useNetwork,
  usePreflight,
  useTxToast,
  useWaitForConfirmation,
  useWallet,
  type SignedAuth,
  type TokenDeployedResult,
  type TokenSentResult,
  type TransferSentResult,
  type WidgetStatus,
} from '@splendor/connect-react';
import { Btn, Card, Field, Hint, Result, Row, toJson, useAction } from './ui';

const SAMPLE_A = 'SQ:' + 'AB'.repeat(32);
const SAMPLE_B = 'SQ:' + 'cd'.repeat(32);

export function OfflineHelpersCard({ step }: { step: number }) {
  const [addr, setAddr] = useState(SAMPLE_A);
  const [a, setA] = useState('1.50');
  const [b, setB] = useState('1.5');
  const [to, setTo] = useState(SAMPLE_B);
  const [amount, setAmount] = useState('2');
  const [balance, setBalance] = useState('1.5');
  const [icon, setIcon] = useState('data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=');
  const [domain, setDomain] = useState(window.location.origin);
  const [authMsg, setAuthMsg] = useState(() => {
    const now = Math.floor(Date.now() / 1000);
    return createAuthMessage({
      domain: window.location.origin,
      statement: 'Lab check',
      nonce: 'abc123',
      issuedAt: now,
      expirationTime: now + 300,
    });
  });

  return (
    <Card
      id="helpers"
      step={step}
      title="Address, amount and sign-in helpers"
      tag="No wallet"
      checks={[
        'normalizeSqAddress: a mixed-case SQ: address comes back lowercase; 0x…, raw hex and short input return null.',
        'compareDecimal: 1.50 vs 1.5 returns 0, 9.99 vs 10 returns -1, and non-numeric input returns null.',
        'reviewTransfer: amount 2 with balance 1.5 returns { ok: false, reason: "insufficient" }; pasting the self address returns reason "self".',
        'sanitizeIconUrl: an SVG data URL returns null; an https URL is returned unchanged.',
        'checkAuthMessage: the generated message returns ok: true; a different expected domain returns reason "domain"; an Issued At far in the future returns "not-yet-valid".',
      ]}
    >
      <Field label="Address" value={addr} onChange={setAddr} />
      <pre className="lab-out">normalizeSqAddress(Address): {toJson(normalizeSqAddress(addr))}</pre>

      <Row>
        <Field label="A" value={a} onChange={setA} />
        <Field label="B" value={b} onChange={setB} />
      </Row>
      <pre className="lab-out">compareDecimal(A, B): {toJson(compareDecimal(a, b))}</pre>

      <Row>
        <Field label="To" value={to} onChange={setTo} />
      </Row>
      <Row>
        <Field label="Amount" value={amount} onChange={setAmount} />
        <Field label="Balance" value={balance} onChange={setBalance} />
      </Row>
      <pre className="lab-out">
        reviewTransfer(To, Amount, Balance): {toJson(reviewTransfer({ to, amount, balance, self: SAMPLE_A }))}
      </pre>
      <Hint>The self address for this check is the sample address above ({SAMPLE_A.slice(0, 12)}…).</Hint>

      <Field label="Icon URL" value={icon} onChange={setIcon} />
      <pre className="lab-out">sanitizeIconUrl(Icon URL): {toJson(sanitizeIconUrl(icon))}</pre>

      <Field label="Expected domain" value={domain} onChange={setDomain} />
      <label className="lab-field">
        <span>Auth message (editable)</span>
        <textarea
          className="mono"
          rows={7}
          value={authMsg}
          onChange={(e) => setAuthMsg(e.target.value)}
          spellCheck={false}
        />
      </label>
      <pre className="lab-out">checkAuthMessage(message, expected domain): {toJson(checkAuthMessage(authMsg, { domain }))}</pre>
    </Card>
  );
}

export function ConnectCard({ step }: { step: number }) {
  const { status, connectingStep, activeConnectorId, errorMessage, connectors, openModal, disconnect } =
    useConnect();
  const dto = useAccountDto();
  return (
    <Card
      id="connect"
      step={step}
      title="Connect, lock and disconnect (ConnectButton, useConnect)"
      tag="Wallet"
      checks={[
        'Connect opens the modal. It lists the Splendor Quantum Wallet when the extension is installed, and always offers a native seed.',
        'Seed: Create a new wallet, Reveal, save the seed, tick "I saved this seed in a safe place", then Unlock. status becomes "connected".',
        'accountDto shows your SQ: address with postQuantumSecure: true.',
        'Reload the page: an extension session reconnects on its own; a seed session does not, because the seed is never stored.',
        'Extension: lock the wallet in the extension. status becomes "locked" and the widgets show Unlock.',
        'Disconnect: status becomes "disconnected" and the widgets show Connect.',
      ]}
    >
      <Row>
        <ConnectButton />
        <Btn kind="ghost" onClick={() => openModal('connect')}>
          Open connect modal
        </Btn>
        <Btn kind="ghost" onClick={() => openModal('account')} disabled={status !== 'connected'}>
          Open account modal
        </Btn>
        <Btn kind="ghost" onClick={() => void disconnect()} disabled={status === 'disconnected'}>
          Disconnect
        </Btn>
      </Row>
      <pre className="lab-out">
        {toJson({
          status,
          connectingStep,
          activeConnectorId,
          errorMessage,
          connectors: connectors.map((c) => c.id),
          accountDto: dto,
        })}
      </pre>
    </Card>
  );
}

export function NetworkCard({ step, expected }: { step: number; expected: string }) {
  const { network, networkId, chainId, networks, switching, walletChainId, walletMismatch } =
    useNetwork();
  const { provider } = useWallet();
  const status = useAction();
  return (
    <Card
      id="network"
      step={step}
      title="Network and wrong-network banners (NetworkBadge, useNetwork)"
      tag="No wallet"
      checks={[
        `The badge shows the app's network. This lab expects "${expected}".`,
        'App banner: pick another network in the badge. A banner offers "Switch to …"; clicking it switches back and the banner goes away.',
        'Get node status: returns the chain_id and height reported by the RPC node.',
        'Wallet banner (extension only): walletChainId matches chainId. Switch the extension to another network: a "Your wallet is on …, but this app is on …" banner appears and sends are refused. Switch back and it goes away.',
      ]}
    >
      <Row>
        <NetworkBadge />
        <Btn kind="ghost" onClick={() => void status.run(() => provider!.getStatus())} disabled={!provider}>
          Get node status
        </Btn>
      </Row>
      <WrongNetworkBanner expectedNetworkId={expected} />
      <pre className="lab-out">
        {toJson({
          networkId,
          chainId,
          walletChainId,
          walletMismatch,
          switching,
          rpc: network?.rpcUrls,
          explorer: network?.explorerUrl,
          selectable: networks.map((n) => n.id),
        })}
      </pre>
      <Result {...status} />
    </Card>
  );
}

export function AccountCardSection({ step }: { step: number }) {
  const { balance, balanceSPLD, loading, errorMessage, refresh } = useBalance();
  const { preflight, status, result, errorMessage: pfErr } = usePreflight();
  const [amt, setAmt] = useState('0.01');
  return (
    <Card
      id="account"
      step={step}
      title="Account, balance and preflight (AccountCard, useBalance, usePreflight)"
      tag="Wallet + SPLD"
      checks={[
        'AccountCard shows the shortened SQ: address, a Post-quantum badge and the SPLD balance.',
        'Copy puts the full address on the clipboard.',
        'The explorer link opens <explorer>/address/<your SQ: address>.',
        'Fund the address (test coins on testnet or devnet), click Refresh, and the balance updates.',
        'Preflight 0.01: returns sufficient, balance_spld, fee and nonce. sufficient is true when the balance covers the amount plus the fee.',
      ]}
    >
      <div className="lab-widget">
        <AccountCard />
      </div>
      <Row>
        <Btn kind="ghost" onClick={() => void refresh()} disabled={loading}>
          {loading ? 'Refreshing…' : 'Refresh balance'}
        </Btn>
      </Row>
      <pre className="lab-out">{toJson({ balance, balanceSPLD, errorMessage })}</pre>
      <Row>
        <Field label="Preflight amount (SPLD)" value={amt} onChange={setAmt} />
        <Btn onClick={() => void preflight({ amountSPLD: amt }).catch(() => undefined)}>Preflight</Btn>
      </Row>
      <Result status={status} data={result} error={pfErr} />
    </Card>
  );
}

/** Wires a widget's onStatusChange / onSuccess / onError into a lab Result panel. */
function useWidgetLog<T>() {
  const [status, setStatus] = useState<WidgetStatus>('idle');
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const props = {
    onStatusChange: (s: WidgetStatus) => {
      setStatus(s);
      if (s === 'pending') {
        setData(null);
        setError(null);
      }
    },
    onSuccess: (r: T) => setData(r),
    onError: (e: Error) => setError(e.message),
  };
  return { status, data, error, props };
}

export function TransferCard({ step }: { step: number }) {
  const log = useWidgetLog<TransferSentResult>();
  return (
    <Card
      id="transfer"
      step={step}
      title="SPLD transfer (TransferWidget)"
      tag="Wallet + SPLD"
      checks={[
        'Type 0x… or a short SQ: address: "Enter a valid SQ: address (0x and raw hex are not accepted)" and Review stays disabled.',
        'Paste your own address: "You cannot send to your own address".',
        'Enter more than the balance: "Amount exceeds your SPLD balance". Max fills in the full balance.',
        'Valid recipient and a small amount, then Review: shows To, Amount and Network. Back returns to the form.',
        'Confirm send: the wallet asks for approval, then the widget shows "Confirmed: <hash>" with an Explorer link.',
        'After a send, the recipient is listed under "Recent:"; clicking it fills in the To field.',
        'The panel below the widget follows onStatusChange (Working…, then Done or Failed) and shows the onSuccess result.',
        'With the wallet locked or disconnected, the widget shows an Unlock / Connect button instead of the form.',
      ]}
    >
      <div className="lab-widget">
        <TransferWidget defaultAmount="0.001" {...log.props} />
      </div>
      <Result status={log.status} data={log.data} error={log.error} />
    </Card>
  );
}

export function TokenCard({ step }: { step: number }) {
  const deployLog = useWidgetLog<TokenDeployedResult>();
  const tokenLog = useWidgetLog<TokenSentResult>();
  return (
    <Card
      id="qrc20"
      step={step}
      title="QRC-20 token (DeployTokenWidget, TokenPanel)"
      tag="Wallet + SPLD"
      checks={[
        'The deploy widget is titled "Deploy a QRC-20" and the button reads "Deploy token" (both set with labels).',
        'Deploy with a name, symbol, supply and decimals: the widget shows "Token: <address>" and "Confirmed: <hash>" with copy and Explorer.',
        'Paste the token address into the token panel: the balance shows the full supply with the token symbol.',
        'Transfer runs the same checks as the SPLD transfer (0x, self, over-balance) and has the same Review step.',
        'Confirm: shows "Submitted: <hash>" or "Confirmed: <hash>", and the balance drops by the amount sent.',
        'Each widget\'s onSuccess result (token address, transfer hash) appears in the panel under it.',
      ]}
    >
      <div className="lab-grid">
        <div>
          <DeployTokenWidget
            defaultName="Lab Token"
            defaultSymbol="LAB"
            defaultSupply="1000"
            showDecimals
            labels={{ title: 'Deploy a QRC-20', deploy: 'Deploy token' }}
            {...deployLog.props}
          />
          <Result status={deployLog.status} data={deployLog.data} error={deployLog.error} />
        </div>
        <div>
          <TokenPanel {...tokenLog.props} />
          <Result status={tokenLog.status} data={tokenLog.data} error={tokenLog.error} />
        </div>
      </div>
    </Card>
  );
}

export function SignInCard({ step }: { step: number }) {
  const [last, setLast] = useState<SignedAuth | null>(null);
  const [badDomain, setBadDomain] = useState(false);
  return (
    <Card
      id="signin"
      step={step}
      title="Sign-in with an ML-DSA signature (SignInButton)"
      tag="Wallet"
      checks={[
        'Sign in: the wallet asks you to sign, then the widget shows "Signed in as SQ:…" and the signed payload appears below.',
        'The signed message (Splendor Connect v2) contains the domain, your address, chain id, nonce, Issued At and Expiration Time.',
        'Tick "Use a challenge for another site": Sign in shows "Sign-in challenge is for a different site" and the wallet is never asked.',
        'Reject the request in the wallet: an error is shown and there is no "Signed in" line.',
      ]}
    >
      <label className="lab-inline">
        <input type="checkbox" checked={badDomain} onChange={(e) => setBadDomain(e.target.checked)} />
        Use a challenge for another site (https://evil.example)
      </label>
      <div className="lab-widget">
        <SignInButton
          key={badDomain ? 'bad' : 'good'}
          statement="Sign in to the Splendor feature lab"
          domain={badDomain ? 'https://evil.example' : undefined}
          nonce={async () => crypto.randomUUID()}
          onSignedIn={setLast}
        />
      </div>
      {last ? <pre className="lab-out">{toJson(last)}</pre> : null}
    </Card>
  );
}

export function ToastCard({ step }: { step: number }) {
  const { push, clear } = useTxToast();
  return (
    <Card
      id="toasts"
      step={step}
      title="Transaction toasts (TxToastHost, useTxToast)"
      tag="No wallet"
      checks={[
        'Pending: a toast appears and stays until dismissed.',
        'Success and Error: the toast closes on its own after 8 seconds (autoDismissMs default). × closes it straight away.',
        'Clear removes every toast.',
      ]}
    >
      <Row>
        <Btn kind="ghost" onClick={() => push({ status: 'pending', hash: null, explorerUrl: null, errorMessage: null })}>
          Pending
        </Btn>
        <Btn
          kind="ghost"
          onClick={() =>
            push({
              status: 'success',
              title: 'Sent 0.01 SPLD',
              hash: '0x' + 'ab'.repeat(32),
              explorerUrl: null,
              errorMessage: null,
            })
          }
        >
          Success
        </Btn>
        <Btn
          kind="ghost"
          onClick={() =>
            push({ status: 'error', hash: null, explorerUrl: null, errorMessage: 'Insufficient balance' })
          }
        >
          Error
        </Btn>
        <Btn kind="ghost" onClick={clear}>
          Clear
        </Btn>
      </Row>
    </Card>
  );
}

export function ConfirmationCard({ step }: { step: number }) {
  const { address } = useAccount();
  const { provider } = useWallet();
  const nonce = useAction<number>();
  const { wait, status, error } = useWaitForConfirmation();
  return (
    <Card
      id="confirm"
      step={step}
      title="Nonce and confirmation (useWaitForConfirmation)"
      tag="Wallet + SPLD"
      checks={[
        'Read nonce: returns the account nonce as a number.',
        'Wait for next tx, then send a transfer from card 6 within 60 seconds: the wait finishes once the account nonce moves past the value read.',
        'Wait for next tx without sending anything: fails with a timeout after 60 seconds.',
      ]}
    >
      <Row>
        <Btn
          kind="ghost"
          disabled={!provider || !address}
          onClick={() => void nonce.run(async () => (await provider!.getNonce(address!)).nonce)}
        >
          Read nonce
        </Btn>
        <Btn
          disabled={!provider || !address || nonce.data == null || status === 'pending'}
          onClick={() =>
            void wait({ from: address!, nonceBefore: nonce.data!, timeoutMs: 60_000 }).catch(
              () => undefined,
            )
          }
        >
          {status === 'pending' ? 'Waiting…' : 'Wait for next tx'}
        </Btn>
      </Row>
      <Result {...nonce} />
      <Result status={status} data="Nonce advanced: the transaction is confirmed." error={error?.message} />
    </Card>
  );
}

/** A custom "tip jar" built only from exported parts, as an SDK user would. */
function TipJar() {
  const { address } = useAccount();
  const { balanceSPLD } = useBalance();
  const { network } = useNetwork();
  const { sendTransfer, status, hash, confirmed, explorerUrl, error, reset } = useSendTransfer();
  const form = useTransferForm({ balance: balanceSPLD, self: address, defaultAmount: '0.001' });

  const confirm = async () => {
    if (!form.review.ok) return;
    reset();
    try {
      await sendTransfer({ to: form.review.to, amountSPLD: form.review.amount, waitForConfirmation: true });
      form.remember(form.review.to);
      form.cancelReview();
    } catch {
      /* shown via `error` below */
    }
  };

  return (
    <div className="sqc-widget lab-tip-jar" data-status={status}>
      <h3 className="sqc-widget__title">Tip jar</h3>
      <WalletGate disconnectedText="Connect to leave a tip.">
        {form.reviewing && form.review.ok ? (
          <ReviewPanel
            to={form.review.to}
            amount={form.review.amount}
            symbol="SPLD"
            networkName={network?.name}
            pending={status === 'pending'}
            onBack={form.cancelReview}
            onConfirm={() => void confirm()}
            labels={{ confirmSend: 'Send tip' }}
          />
        ) : (
          <>
            <AddressInput
              label="Creator (SQ:…)"
              value={form.to}
              onChange={form.setTo}
              self={address}
              recents={form.recents}
            />
            <AmountInput
              label="Tip (SPLD)"
              value={form.amount}
              onChange={form.setAmount}
              balance={balanceSPLD}
              symbol="SPLD"
            />
            <button
              type="button"
              className="sqc-btn sqc-btn--primary sqc-btn--block"
              disabled={!form.canReview}
              onClick={form.startReview}
            >
              Review tip
            </button>
          </>
        )}
        <TxResult hash={hash} confirmed={confirmed} explorerUrl={explorerUrl} />
        {error ? (
          <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
            {connectErrorMessage(error)}
          </p>
        ) : null}
      </WalletGate>
    </div>
  );
}

export function ComposeCard({ step }: { step: number }) {
  return (
    <Card
      id="compose"
      step={step}
      title="Build your own (WalletGate, useTransferForm, AddressInput, AmountInput, TxResult)"
      tag="Wallet + SPLD"
      checks={[
        'Disconnected: the card shows "Connect to leave a tip." and a Connect button. Locked: an Unlock button.',
        'The Creator and Tip fields reject 0x addresses, self-sends and amounts above the balance, like the stock widget.',
        'Max fills in the balance; Review tip shows To, Amount and Network, and the confirm button reads "Send tip".',
        'After the wallet approves, the card shows "Confirmed: <hash>" with copy and Explorer, using the same styles as the stock widgets.',
      ]}
    >
      <div className="lab-widget">
        <TipJar />
      </div>
      <Hint>
        This card is about 80 lines of app code: the parts and the headless hook do validation, review and the result
        display. See the guide section on building your own widget.
      </Hint>
    </Card>
  );
}
