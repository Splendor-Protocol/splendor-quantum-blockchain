const STORAGE_KEY = 'splendor.connect.lastConnectorId';

export function getLastConnectorId(): string | null {
  try {
    if (typeof sessionStorage === 'undefined') return null;
    return sessionStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

export function setLastConnectorId(id: string | null): void {
  try {
    if (typeof sessionStorage === 'undefined') return;
    if (id == null) sessionStorage.removeItem(STORAGE_KEY);
    else sessionStorage.setItem(STORAGE_KEY, id);
  } catch {
    /* ignore quota / private mode */
  }
}
