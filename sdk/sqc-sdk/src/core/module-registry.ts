/**
 * Open module registry — add Programs without editing Session bind tables by hand.
 *
 * Built-ins register from modules.ts. Addons call registerModule({ id, create }).
 */
import type { Connection } from './connection.js';
import type { Program } from './program.js';
import type { WalletLike, BoundClient } from '../bound.js';
import { BridgeError } from '../errors.js';

export type ModuleFactory<T = Program> = (connection: Connection, address: string) => T;

export type ModuleDefinition<T extends { withSigner(w: WalletLike): BoundClient<T> } = Program & {
  withSigner(w: WalletLike): BoundClient<Program>;
}> = {
  id: string;
  create: ModuleFactory<T>;
  /** Optional human label for docs / UI. */
  label?: string;
};

const registry = new Map<string, ModuleDefinition>();

export function defineModule<T extends { withSigner(w: WalletLike): BoundClient<T> }>(
  def: ModuleDefinition<T>,
): ModuleDefinition<T> {
  return def;
}

export function registerModule<T extends { withSigner(w: WalletLike): BoundClient<T> }>(
  def: ModuleDefinition<T>,
): void {
  const id = def.id.trim();
  if (!id) throw BridgeError.rpc('registerModule: id is required');
  registry.set(id, def as unknown as ModuleDefinition);
}

export function getModule(id: string): ModuleDefinition | undefined {
  return registry.get(id);
}

export function unregisterModule(id: string): void {
  registry.delete(id);
}

export function listModules(): ModuleDefinition[] {
  return [...registry.values()];
}

export function createModuleClient(
  id: string,
  connection: Connection,
  address: string,
): Program {
  const def = registry.get(id);
  if (!def) {
    throw BridgeError.rpc(`Unknown module "${id}" — registerModule() first`);
  }
  return def.create(connection, address) as Program;
}

export function bindRegisteredModules(
  connection: Connection,
  wallet?: WalletLike | null,
): Record<string, (address: string) => unknown> {
  const out: Record<string, (address: string) => unknown> = {};
  for (const def of registry.values()) {
    out[def.id] = (address: string) => {
      const client = def.create(connection, address) as {
        withSigner(w: WalletLike): BoundClient<typeof client>;
      };
      return wallet ? client.withSigner(wallet) : client;
    };
  }
  return out;
}

/** Test helper — clear registry (re-register built-ins after). */
export function _resetModuleRegistryForTests(): void {
  registry.clear();
}
