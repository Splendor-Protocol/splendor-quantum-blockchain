/** Minimal typed event emitter for bridge connection lifecycle. */

export type BridgeEventMap = {
  connect: { address: string; evmAddress?: string };
  disconnect: Record<string, never>;
  /** ML-DSA-65 keys cleared; session slot retained for soft reconnect. */
  lock: Record<string, never>;
  accountsChanged: { address: string; evmAddress?: string };
  error: { code: string; message: string };
};

type Handler<T> = (payload: T) => void;

export class BridgeEmitter {
  private listeners = new Map<string, Set<Handler<unknown>>>();

  on<K extends keyof BridgeEventMap>(event: K, handler: Handler<BridgeEventMap[K]>): () => void {
    let set = this.listeners.get(event);
    if (!set) {
      set = new Set();
      this.listeners.set(event, set);
    }
    set.add(handler as Handler<unknown>);
    return () => this.off(event, handler);
  }

  off<K extends keyof BridgeEventMap>(event: K, handler: Handler<BridgeEventMap[K]>): void {
    this.listeners.get(event)?.delete(handler as Handler<unknown>);
  }

  emit<K extends keyof BridgeEventMap>(event: K, payload: BridgeEventMap[K]): void {
    const set = this.listeners.get(event);
    if (!set) return;
    for (const h of set) {
      try {
        h(payload);
      } catch {
        /* ignore listener errors */
      }
    }
  }

  removeAllListeners(): void {
    this.listeners.clear();
  }
}
