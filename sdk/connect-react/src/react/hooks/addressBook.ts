import { useCallback, useMemo, useSyncExternalStore } from 'react';
import {
  getAllContacts,
  getContacts,
  removeContact,
  saveContact,
  subscribeContacts,
  type Contact,
  type SaveContactResult,
} from '../../core/addressBook.js';
import { useConnectState } from '../context.js';

export type { Contact, SaveContactResult } from '../../core/addressBook.js';

const EMPTY: Contact[] = [];

/** Address book for the current network (labels sorted A–Z), stored in this browser only. */
export function useAddressBook(): {
  contacts: Contact[];
  save: (address: string, label: string) => SaveContactResult;
  remove: (address: string) => void;
  /** Label for an address on this network, or null. */
  labelOf: (address: string | null | undefined) => string | null;
} {
  const networkId = useConnectState((s) => s.network?.id ?? null);
  const all = useSyncExternalStore(subscribeContacts, getAllContacts, () => EMPTY);
  // `all` changes identity on every write, which is what re-derives this list.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const contacts = useMemo(() => getContacts(networkId), [all, networkId]);
  const save = useCallback(
    (address: string, label: string): SaveContactResult =>
      networkId ? saveContact({ address, label, network: networkId }) : { ok: false, reason: 'invalid' },
    [networkId],
  );
  const remove = useCallback((address: string) => {
    if (networkId) removeContact(address, networkId);
  }, [networkId]);
  const labelOf = useCallback(
    (address: string | null | undefined) => {
      if (!address) return null;
      const a = address.trim().toLowerCase();
      return contacts.find((c) => c.address.toLowerCase() === a)?.label ?? null;
    },
    [contacts],
  );
  return { contacts, save, remove, labelOf };
}
