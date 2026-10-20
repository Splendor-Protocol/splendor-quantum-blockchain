import { useEffect, useState } from 'react';
import { txExplorerUrl } from '@splendor/sqc-sdk/networks';
import {
  AddressDisplay,
  TxResult,
  WidgetShell,
  addressExplorerUrl,
  connectErrorMessage,
  useAccount,
  useDeployCollection,
  useNetwork,
  useQRC1155,
} from '@splendor/connect-react';

export type NftDropProps = {
  title?: string;
  /** QRC-1155 collection address. Empty: the block offers to deploy a new collection. */
  collection?: string;
  /** Token id minted by this drop. */
  tokenId?: string;
  /** Amount minted per click. */
  amount?: string;
  collectionName?: string;
};

/** QRC-1155 drop: deploy a collection (or use one), mint to yourself and show your balance. */
export function NftDrop({
  title = 'NFT drop',
  collection = '',
  tokenId = '1',
  amount = '1',
  collectionName = 'My Collection',
}: NftDropProps) {
  const { address } = useAccount();
  const { network } = useNetwork();
  const deployer = useDeployCollection();
  const active = collection || deployer.address || null;
  const nft = useQRC1155(active);
  const [owned, setOwned] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  // A collection deployed here exists on one network only; forget it when the network changes.
  const { reset: resetDeployer } = deployer;
  useEffect(() => resetDeployer(), [network?.id, resetDeployer]);

  useEffect(() => {
    setOwned(null);
    if (!active || !address) return;
    let live = true;
    nft
      .balanceOf(address, tokenId)
      .then((b) => live && setOwned(b))
      .catch(() => live && setOwned(null));
    return () => {
      live = false;
    };
  }, [active, address, tokenId, tick, nft.balanceOf]);

  const deployHref = network && deployer.hash ? txExplorerUrl(network, deployer.hash) : null;
  const error = nft.error ?? deployer.error;

  return (
    <WidgetShell kind="nft-drop" title={title} status={nft.status === 'pending' || deployer.status === 'pending' ? 'pending' : 'idle'}>
      {!active ? (
        <>
          <p className="sqc-widget__msg">No collection yet. Deploy one to start the drop.</p>
          <button
            type="button"
            className="sqc-btn sqc-btn--primary sqc-btn--block"
            disabled={deployer.status === 'pending'}
            onClick={() => void deployer.deploy({ name: collectionName, waitForConfirmation: true }).catch(() => undefined)}
          >
            {deployer.status === 'pending' ? 'Deploying…' : `Deploy "${collectionName}"`}
          </button>
        </>
      ) : (
        <>
          <p className="sqc-widget__msg" data-testid="dapp-nft-collection">
            Collection <AddressDisplay value={active} href={addressExplorerUrl(network?.explorerUrl, active)} />
          </p>
          {deployer.hash ? <TxResult hash={deployer.hash} explorerUrl={deployHref} /> : null}
          <p className="sqc-widget__msg" data-testid="dapp-nft-owned">
            You own {owned ?? '…'} of token #{tokenId}
          </p>
          <button
            type="button"
            className="sqc-btn sqc-btn--primary sqc-btn--block"
            disabled={!address || nft.status === 'pending'}
            onClick={() =>
              address &&
              void nft
                .mint(address, tokenId, amount)
                .then(() => setTick((t) => t + 1))
                .catch(() => undefined)
            }
          >
            {nft.status === 'pending' ? 'Minting…' : `Mint ${amount}`}
          </button>
          {nft.hash ? <TxResult hash={nft.hash} explorerUrl={nft.explorerUrl} /> : null}
        </>
      )}
      {error ? (
        <p className="sqc-widget__msg sqc-widget__msg--error" role="alert">
          {connectErrorMessage(error)}
        </p>
      ) : null}
    </WidgetShell>
  );
}
