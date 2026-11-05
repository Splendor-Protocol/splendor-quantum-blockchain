import type { Connection } from './connection.js';
import type { Transport } from './transport/types.js';

/** The slice of a Transport the read-only module clients (verify, validators) need. */
export type ChainIo = Pick<Transport, 'get' | 'post'> & Partial<Pick<Transport, 'moduleCall'>>;

/** A Connection's transport, or the transport / plain get-post object itself. */
export function chainIo(source: Connection | Transport | ChainIo): ChainIo {
  if ('transport' in source && source.transport && typeof source.transport.get === 'function') {
    return source.transport;
  }
  return source as ChainIo;
}
