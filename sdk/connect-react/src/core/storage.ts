const CONNECTOR_KEY = 'splendor.connect.lastConnectorId';
const NETWORK_KEY = 'splendor.connect.lastNetworkId';
const CHANNEL = 'splendor-connect';

function readKey(store: Storage | undefined, key: string): string | null {
  try {
    if (!store) return null;
    return store.getItem(key);
  } catch {
    return null;
  }
}

function writeKey(store: Storage | undefined, key: string, id: string | null): void {
  try {
    if (!store) return;
    if (id == null) store.removeItem(key);
    else store.setItem(key, id);
  } catch {
    /* ignore */
  }
}

function localStorageOrUndefined(): Storage | undefined {
  try {
    if (typeof localStorage === 'undefined') return undefined;
    return localStorage;
  } catch {
    return undefined;
  }
}

function sessionStorageOrUndefined(): Storage | undefined {
  try {
    if (typeof sessionStorage === 'undefined') return undefined;
    return sessionStorage;
  } catch {
    return undefined;
  }
}

/** Migrate sessionStorage → localStorage once, then read localStorage. */
export function getLastConnectorId(): string | null {
  const local = localStorageOrUndefined();
  const session = sessionStorageOrUndefined();
  const fromLocal = readKey(local, CONNECTOR_KEY);
  if (fromLocal) return fromLocal;
  const fromSession = readKey(session, CONNECTOR_KEY);
  if (fromSession) {
    writeKey(local, CONNECTOR_KEY, fromSession);
    writeKey(session, CONNECTOR_KEY, null);
    return fromSession;
  }
  return null;
}

export function setLastConnectorId(id: string | null): void {
  writeKey(localStorageOrUndefined(), CONNECTOR_KEY, id);
  writeKey(sessionStorageOrUndefined(), CONNECTOR_KEY, null);
}

export function getLastNetworkId(): string | null {
  return readKey(localStorageOrUndefined(), NETWORK_KEY);
}

export function setLastNetworkId(id: string | null): void {
  writeKey(localStorageOrUndefined(), NETWORK_KEY, id);
}

export type ConnectBroadcastMessage =
  | { type: 'disconnect' }
  | { type: 'connected'; connectorId: string; address: string };

export function broadcastConnectEvent(msg: ConnectBroadcastMessage): void {
  try {
    if (typeof BroadcastChannel === 'undefined') return;
    const ch = new BroadcastChannel(CHANNEL);
    ch.postMessage(msg);
    ch.close();
  } catch {
    /* ignore */
  }
}

export function subscribeConnectBroadcast(
  handler: (msg: ConnectBroadcastMessage) => void,
): () => void {
  try {
    if (typeof BroadcastChannel === 'undefined') return () => undefined;
    const ch = new BroadcastChannel(CHANNEL);
    const onMsg = (ev: MessageEvent) => {
      const data = ev.data as ConnectBroadcastMessage;
      if (data && typeof data === 'object' && 'type' in data) handler(data);
    };
    ch.addEventListener('message', onMsg);
    return () => {
      ch.removeEventListener('message', onMsg);
      ch.close();
    };
  } catch {
    return () => undefined;
  }
}
